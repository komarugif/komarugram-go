// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"sync"
	"testing"
	"time"

	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// sendAndWait sends text from the composer of the harness's chat, and frames
// until the message shows in the history. It returns the message's id.
func sendAndWait(t *testing.T, h *composerHarness, text string) model.MessageID {
	t.Helper()
	before := len(h.p.messages)
	h.p.composer.submit(h.chat, model.OutgoingMessage{Text: text})
	for deadline := time.Now().Add(3 * time.Second); len(h.p.messages) == before && time.Now().Before(deadline); {
		time.Sleep(2 * time.Millisecond)
		h.frame()
	}
	if len(h.p.messages) == before {
		t.Fatal("the message did not show in the history")
	}
	return h.p.messages[len(h.p.messages)-1].Key.MessageID
}

func newSendHarness(t *testing.T) *composerHarness {
	h := newComposerHarness(t)
	h.chat = 2
	h.animate = true
	for range 3 {
		h.frame()
	}
	return h
}

// A message sent flies from the composer to its place, and is there in less
// than a second.
func TestSentMessageFliesToItsPlace(t *testing.T) {
	h := newSendHarness(t)
	id := sendAndWait(t, h, "hello")
	if h.p.flights[id] == nil {
		t.Fatal("the message sent does not fly")
	}
	for range 60 {
		h.frame()
	}
	if len(h.p.flights) != 0 {
		t.Fatal("the flight has no end")
	}
	// One message for one send: the next that comes is not the composer's.
	if h.p.composer.takeSent(h.chat) {
		t.Fatal("the note of the message sent is kept after it has flown")
	}
}

// With the animations off, in the setting or by the window, the message is
// in its place at once, as it was, and nothing is left to fly later.
func TestSentMessageDoesNotFlyWithoutAnimations(t *testing.T) {
	for name, set := range map[string]func(*composerHarness){
		"the setting": func(h *composerHarness) { h.still = true },
		"the page":    func(h *composerHarness) { h.animate = false },
	} {
		h := newSendHarness(t)
		set(h)
		id := sendAndWait(t, h, "hello")
		if len(h.p.flights) != 0 || h.p.flights[id] != nil {
			t.Fatalf("%s is off, and the message flies", name)
		}
	}
}

// A history that shows an older place does not fly one either; a message
// that the composer did not send rises to its place, as one that came does.
func TestOnlyWhatIsSentAtTheEndFlies(t *testing.T) {
	h := newSendHarness(t)
	h.p.list.Position.BeforeEnd = true
	h.p.list.Position.First = 2
	h.frame()
	sendAndWait(t, h, "far from the end")
	if len(h.p.flights) != 0 {
		t.Fatal("a message flies into a history that is not at its end")
	}

	h = newSendHarness(t)
	before := len(h.p.messages)
	// Sent from another device: the store has it, and the composer no note.
	store := h.p.source.(interface {
		Send(context.Context, int64, model.OutgoingMessage) error
	})
	if err := store.Send(context.Background(), h.chat, model.OutgoingMessage{RandomID: 77, Text: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); len(h.p.messages) == before && time.Now().Before(deadline); {
		time.Sleep(2 * time.Millisecond)
		h.frame()
	}
	if len(h.p.messages) == before {
		t.Fatal("the message did not come")
	}
	id := h.p.messages[len(h.p.messages)-1].Key.MessageID
	if f := h.p.flights[id]; f == nil || !f.arrival || len(h.p.flights) != 1 {
		t.Fatalf("the message from another device does not rise: %v", h.p.flights)
	}
}

// A send that failed does not leave its note to make the next message fly.
func TestFailedSendLeavesNoNote(t *testing.T) {
	h := newSendHarness(t)
	c := h.p.composer
	c.source = failingComposer{make(chan model.OutgoingMessage, 2)}
	c.submit(h.chat, model.OutgoingMessage{Text: "no connection"})
	d := c.draft(h.chat)
	for deadline := time.Now().Add(3 * time.Second); d.sending && time.Now().Before(deadline); {
		time.Sleep(2 * time.Millisecond)
		h.frame()
	}
	if d.err == nil {
		t.Fatal("the send did not fail")
	}
	if c.takeSent(h.chat) {
		t.Fatal("a note is left of a message that was not sent")
	}
}

// savedStore is a store whose messages are not marked outgoing, as Telegram
// has them in Saved Messages.
type savedStore struct{ *mockstore.Store }

func unmarked(h model.History) model.History {
	h.Messages = append([]model.Message(nil), h.Messages...)
	for i := range h.Messages {
		h.Messages[i].Outgoing = false
	}
	return h
}

func (s savedStore) History(chat int64) model.History { return unmarked(s.Store.History(chat)) }
func (s savedStore) HistorySince(chat int64, revision uint64) (model.History, bool) {
	h, fresh := s.Store.HistorySince(chat, revision)
	return unmarked(h), fresh
}

func newUnmarkedHarness(t *testing.T, kind model.ChatKind) *composerHarness {
	p := newChatPage(savedStore{mockstore.New(time.Now(), 0)}, func() {})
	p.images = &imageOps{}
	t.Cleanup(p.Close)
	h := &composerHarness{p: p, now: time.Now(), size: image.Pt(680, 720), chat: 2, kind: kind, animate: true}
	for range 4 {
		h.frame()
	}
	return h
}

// In Saved Messages Telegram does not mark the user's messages as outgoing,
// and they fly all the same; in another chat such a message is somebody
// else's, and does not take the flight of the message that was sent: it
// rises to its place as one that came.
func TestSavedMessagesFly(t *testing.T) {
	h := newUnmarkedHarness(t, model.KindSaved)
	id := sendAndWait(t, h, "a note")
	if h.p.flights[id] == nil {
		t.Fatal("a message sent to Saved Messages does not fly")
	}

	h = newUnmarkedHarness(t, model.KindUser)
	id = sendAndWait(t, h, "hello")
	if f := h.p.flights[id]; f == nil || !f.arrival {
		t.Fatal("a message that is not the user's flies from the composer, or does not rise")
	}
	if !h.p.composer.takeSent(h.chat) {
		t.Fatal("somebody else's message took the note of the message sent")
	}
}

// relayStore holds a message sent on its way, as tgstore does, until take
// hands it to the demo, which makes the message Telegram would.
type relayStore struct {
	*mockstore.Store
	mu      sync.Mutex
	pending *model.Message
	out     model.OutgoingMessage
}

func (s *relayStore) Send(_ context.Context, chat int64, out model.OutgoingMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = out
	s.pending = &model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: -(1 << 30)}, Text: out.Text, Outgoing: true, Sending: true, Date: time.Now(), ContentRevision: 1}
	return nil
}

func (s *relayStore) take(chat int64) {
	s.mu.Lock()
	out := s.out
	s.pending = nil
	s.mu.Unlock()
	_ = s.Store.Send(context.Background(), chat, out)
}

func (s *relayStore) History(chat int64) model.History {
	h := s.Store.History(chat)
	h.Messages = append([]model.Message(nil), h.Messages...)
	for i := range h.Messages {
		h.Messages[i].Sending = false
	}
	s.mu.Lock()
	if s.pending != nil {
		h.Messages = append(h.Messages, *s.pending)
	}
	s.mu.Unlock()
	return h
}

func (s *relayStore) HistorySince(chat int64, _ uint64) (model.History, bool) {
	return s.History(chat), true
}

// A message on its way flies from the composer, and the message Telegram
// makes of it goes on with the same flight, not one of its own.
func TestMessageOnItsWayFlies(t *testing.T) {
	store := &relayStore{Store: mockstore.New(time.Now(), 0)}
	p := newChatPage(store, func() {})
	p.images = &imageOps{}
	t.Cleanup(p.Close)
	h := &composerHarness{p: p, now: time.Now(), size: image.Pt(680, 720), chat: 2, kind: model.KindUser, animate: true}
	for range 4 {
		h.frame()
	}
	id := sendAndWait(t, h, "hello")
	flight := h.p.flights[id]
	if id >= 0 || flight == nil {
		t.Fatalf("the message on its way (%d) does not fly", id)
	}
	h.frame()
	store.take(h.chat)
	h.frame()
	last := h.p.messages[len(h.p.messages)-1]
	if last.Sending || last.Key.MessageID <= 0 {
		t.Fatalf("not taken: %+v", last)
	}
	if h.p.flights[last.Key.MessageID] != flight || len(h.p.flights) != 1 {
		t.Fatalf("the flight did not go on: %v", h.p.flights)
	}
}

// A message of another user that comes at the end of the history rises to
// its place; one an older page brings does not.
func TestIncomingMessageRises(t *testing.T) {
	h := newSendHarness(t)
	store := h.p.source.(*mockstore.Store)
	before := len(h.p.messages)
	store.Receive() // the first comes to chat 2
	for deadline := time.Now().Add(3 * time.Second); len(h.p.messages) == before && time.Now().Before(deadline); {
		time.Sleep(2 * time.Millisecond)
		h.frame()
	}
	if len(h.p.messages) == before {
		t.Fatal("the message did not come")
	}
	last := h.p.messages[len(h.p.messages)-1]
	if f := h.p.flights[last.Key.MessageID]; last.Outgoing || f == nil || !f.arrival {
		t.Fatalf("the message that came does not rise: %+v %v", last, h.p.flights)
	}
	for range 60 {
		h.frame()
	}
	if len(h.p.flights) != 0 {
		t.Fatal("the rise has no end")
	}
}

// A message that was on its way and is taken in its place, as the demo
// takes it, does not fly again.
func TestTakenMessageDoesNotFlyAgain(t *testing.T) {
	h := newSendHarness(t)
	id := sendAndWait(t, h, "hello")
	first := h.p.flights[id]
	taken := 0
	for deadline := time.Now().Add(5 * time.Second); taken < 30 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		h.frame()
		if f := h.p.flights[id]; f != nil && f != first {
			t.Fatal("the message flies again")
		}
		if m := h.p.messages[len(h.p.messages)-1]; m.Key.MessageID == id && !m.Sending {
			taken++
		}
	}
	if taken == 0 {
		t.Fatal("the message was not taken")
	}
}
