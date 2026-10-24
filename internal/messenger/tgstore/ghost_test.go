// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// ghostAPI records what is told to Telegram.
type ghostAPI struct {
	mu   sync.Mutex
	told []string
	// fail is how many reads fail before they are told.
	fail int
}

func (g *ghostAPI) client() *tg.Client {
	return composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch r := in.(type) {
		case *tg.MessagesReadHistoryRequest:
			g.told = append(g.told, fmt.Sprint("read ", r.MaxID))
			if g.fail > 0 {
				g.fail--
				return nil, fmt.Errorf("no connection")
			}
			return &tg.MessagesAffectedMessages{}, nil
		case *tg.AccountUpdateStatusRequest:
			g.told = append(g.told, fmt.Sprint("offline ", r.Offline))
			return &tg.BoolTrue{}, nil
		case *tg.MessagesSetTypingRequest:
			g.told = append(g.told, "typing")
			return &tg.BoolTrue{}, nil
		case *tg.MessagesSendMessageRequest:
			return &tg.UpdateShortSentMessage{ID: 60, Date: 10}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
}

// wait returns what was told once n things were, or fails.
func (g *ghostAPI) wait(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		told := append([]string(nil), g.told...)
		g.mu.Unlock()
		if len(told) >= n {
			return told
		}
		if time.Now().After(deadline) {
			t.Fatalf("told %q, want %d things", told, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// Nothing is told without Ghost's leave, but what is asked; with it, each
// thing once.
func TestGhost(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	api := &ghostAPI{}
	s.history.api = api.client()

	s.MarkRead(chat, 10, false)
	s.SetOnline(true)
	s.Typing(chat)
	s.MarkRead(chat, 10, true)
	if told := api.wait(t, 1); len(told) != 1 || told[0] != "read 10" {
		t.Fatalf("without Ghost's leave told %q", told)
	}

	s.SetGhost(model.Ghost{SendRead: true, SendOnline: true, SendTyping: true})
	s.MarkRead(chat, 10, false) // read already
	s.MarkRead(chat, 12, false)
	s.SetOnline(true)
	s.SetOnline(true) // told already
	s.Typing(chat)
	s.Typing(chat) // too soon
	told := api.wait(t, 4)
	time.Sleep(20 * time.Millisecond)
	told = api.wait(t, 4)
	want := map[string]bool{"read 12": true, "offline false": true, "typing": true}
	if len(told) != 4 {
		t.Fatalf("told %q", told)
	}
	for _, one := range told[1:] {
		if !want[one] {
			t.Fatalf("told %q", told)
		}
	}
	// Turning online off goes offline.
	s.SetGhost(model.Ghost{SendRead: true})
	if told := api.wait(t, 5); told[4] != "offline true" {
		t.Fatalf("told %q", told)
	}
}

// Reading a chat counts what is left unread from the history, is told once
// its peer is known, and a message counted after it was read stays read.
func TestMarkReadUnread(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	api := &ghostAPI{}
	s.history.api = api.client()
	s.SetGhost(model.Ghost{SendRead: true})
	s.chats = []model.Chat{{ID: chat, Unread: 3}}
	s.history.top[chat] = 12
	in := func(id model.MessageID) model.Message {
		return model.Message{Key: model.MessageKey{ChatID: chat, MessageID: id}}
	}
	s.history.histories[chat] = &model.History{Messages: []model.Message{in(10), in(11), in(12)}}
	unread := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.chats[0].Unread
	}

	// The peer is not known yet: nothing is told, and nothing is forgotten.
	s.MarkRead(chat, 11, false)
	if n := unread(); n != 3 {
		t.Fatalf("unread %d before the peer is known", n)
	}
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.MarkRead(chat, 11, false)
	if told := api.wait(t, 1); told[0] != "read 11" {
		t.Fatalf("told %q", told)
	}
	if n := unread(); n != 1 {
		t.Fatalf("unread %d, want the one after 11", n)
	}

	// The history marked 13 read before the store counted it.
	s.history.top[chat] = 13
	s.MarkRead(chat, 13, false)
	s.mergeUpdate(in(13))
	if n := unread(); n != 0 {
		t.Fatalf("unread %d after the newest was read", n)
	}
	s.mergeUpdate(in(14))
	if n := unread(); n != 1 {
		t.Fatalf("unread %d after a message not read", n)
	}
}

// A read Telegram was not told, before the connection or on an error, is
// told again: kept as told, the dialogs loaded next brought the unread count
// back, and the history read to the end never took it away.
func TestMarkReadUntoldIsToldAgain(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.SetGhost(model.Ghost{SendRead: true})
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.top[chat] = 12

	// Drawn from the cache before the connection.
	s.MarkRead(chat, 12, false)
	api := &ghostAPI{fail: 1}
	s.history.api = api.client()
	s.MarkRead(chat, 12, false)
	if told := api.wait(t, 1); told[0] != "read 12" {
		t.Fatalf("told %q", told)
	}
	// That one failed; it waits, and is told again after.
	deadline := time.Now().Add(5 * time.Second)
	for s.readUpTo(chat) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("a read that failed is kept as told")
		}
		time.Sleep(time.Millisecond)
	}
	s.MarkRead(chat, 12, false)
	s.ghost.mu.Lock()
	waits := s.ghost.retry[chat].After(time.Now())
	s.ghost.retry[chat] = time.Time{}
	s.ghost.mu.Unlock()
	if !waits {
		t.Fatal("a read that failed does not wait")
	}
	s.MarkRead(chat, 12, false)
	if told := api.wait(t, 2); len(told) != 2 || told[1] != "read 12" {
		t.Fatalf("told %q", told)
	}
}

// A read older than the one told here, which comes after it, leaves the
// unread count as that one made it.
func TestOlderReadKeepsUnread(t *testing.T) {
	s := testStore(t)
	chat := peerID(&tg.PeerUser{UserID: 5})
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.api = (&ghostAPI{}).client()
	s.SetGhost(model.Ghost{SendRead: true})
	s.chats = []model.Chat{{ID: chat, Unread: 3}}
	s.history.top[chat] = 12
	s.MarkRead(chat, 12, false)
	unread := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.chats[0].Unread
	}
	if n := unread(); n != 0 {
		t.Fatalf("unread %d after the newest was read", n)
	}
	s.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateReadHistoryInbox{Peer: &tg.PeerUser{UserID: 5}, MaxID: 10, StillUnreadCount: 2}}})
	if n := unread(); n != 0 {
		t.Fatalf("unread %d after an older read", n)
	}
	s.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateReadHistoryInbox{Peer: &tg.PeerUser{UserID: 5}, MaxID: 12, StillUnreadCount: 1}}})
	if n := unread(); n != 1 {
		t.Fatalf("unread %d, want Telegram's count of the read told", n)
	}
}

// Without read receipts, sending to a chat reads it up to the message sent.
func TestReadOnInteract(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 1, Self: true, FirstName: "Me"}, &tg.User{ID: 5, AccessHash: 55, FirstName: "Ann"}}, nil)
	s.history.histories[chat] = &model.History{}
	api := &ghostAPI{}
	s.history.api = api.client()
	s.SetGhost(model.Ghost{ReadOnInteract: true})
	if err := s.Send(context.Background(), chat, model.OutgoingMessage{RandomID: 1, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if told := api.wait(t, 1); told[0] != "read 60" {
		t.Fatalf("told %q", told)
	}
}
