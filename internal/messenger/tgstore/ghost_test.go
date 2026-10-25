// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
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
	s.SendAction(chat, model.ActionTyping)
	s.MarkRead(chat, 10, true)
	if told := api.wait(t, 1); len(told) != 1 || told[0] != "read 10" {
		t.Fatalf("without Ghost's leave told %q", told)
	}

	s.SetGhost(model.Ghost{SendRead: true, SendOnline: true, SendTyping: true, SendUploads: true})
	s.MarkRead(chat, 10, false) // read already
	s.MarkRead(chat, 12, false)
	s.SetOnline(true)
	s.SetOnline(true) // told already
	s.SendAction(chat, model.ActionTyping)
	s.SendAction(chat, model.ActionTyping) // too soon
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

// Reading a history opened short of its end, at its first unread message,
// lowers the unread count by the messages read: Telegram tells this session
// nothing of its own reads, and the count stood until the end came in sight.
func TestMarkReadShortOfTheEnd(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.history.api = (&ghostAPI{}).client()
	s.SetGhost(model.Ghost{SendRead: true})
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.top[chat] = 100
	s.chats = []model.Chat{{ID: chat, Unread: 90, ReadInbox: 10}}
	var msgs []model.Message
	for id := model.MessageID(1); id <= 40; id++ {
		msgs = append(msgs, model.Message{Key: model.MessageKey{ChatID: chat, MessageID: id}, Outgoing: id == 13})
	}
	s.history.histories[chat] = &model.History{Messages: msgs, HasNewer: true}
	unread := func() model.Chat {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.chats[0]
	}
	s.MarkRead(chat, 15, false)
	if c := unread(); c.Unread != 86 || c.ReadInbox != 15 {
		t.Fatalf("unread %d read to %d, want 86 (the account's 13 is not one) to 15", c.Unread, c.ReadInbox)
	}
	s.MarkRead(chat, 20, false)
	if c := unread(); c.Unread != 81 {
		t.Fatalf("unread %d, want 81", c.Unread)
	}
}

// A page of dialogs read before Telegram had a read this client told counts
// the messages read as unread: it leaves the count the read made. One read
// after it, on another device, is Telegram's to tell.
func TestDialogsKeepReadsTold(t *testing.T) {
	s := testStore(t)
	ann, bob := peerID(&tg.PeerUser{UserID: 5}), peerID(&tg.PeerUser{UserID: 6})
	s.history.peers[ann] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.SetGhost(model.Ghost{SendRead: true})
	s.history.top[ann], s.history.top[bob] = 100, 120
	s.chats = []model.Chat{{ID: ann, Unread: 5, ReadInbox: 95}, {ID: bob, Unread: 4, ReadInbox: 116}}
	api := tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		switch in.(type) {
		case *tg.MessagesReadHistoryRequest:
			*out.(*tg.MessagesAffectedMessages) = tg.MessagesAffectedMessages{}
		case *tg.MessagesGetDialogFiltersRequest:
			*out.(*tg.MessagesDialogFilters) = tg.MessagesDialogFilters{}
		case *tg.MessagesGetDialogsRequest:
			// Read before Telegram had ann's read; bob was read further on
			// the phone.
			out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{
				Dialogs: []tg.DialogClass{
					&tg.Dialog{Peer: &tg.PeerUser{UserID: 5}, TopMessage: 100, ReadInboxMaxID: 95, UnreadCount: 5, NotifySettings: tg.PeerNotifySettings{}},
					&tg.Dialog{Peer: &tg.PeerUser{UserID: 6}, TopMessage: 120, ReadInboxMaxID: 119, UnreadCount: 1, NotifySettings: tg.PeerNotifySettings{}},
				},
				Users: []tg.UserClass{&tg.User{ID: 5, AccessHash: 55, FirstName: "Ann"}, &tg.User{ID: 6, AccessHash: 66, FirstName: "Bob"}},
			}
		default:
			return fmt.Errorf("unexpected %T", in)
		}
		return nil
	}))
	s.history.api = api
	s.MarkRead(ann, 100, false)
	if err := s.loadDialogs(context.Background(), api, 1); err != nil {
		t.Fatal(err)
	}
	got := map[int64]model.Chat{}
	for _, c := range s.Chats() {
		got[c.ID] = c
	}
	if c := got[ann]; c.Unread != 0 || c.ReadInbox != 100 {
		t.Fatalf("the chat read here came back with %d unread, read to %d", c.Unread, c.ReadInbox)
	}
	if c := got[bob]; c.Unread != 1 || c.ReadInbox != 119 {
		t.Fatalf("the chat read on the phone has %d unread, read to %d; want Telegram's 1 to 119", c.Unread, c.ReadInbox)
	}
}

// A chat read to its end has nothing unread, whatever brings its count
// back after the read was told: the count goes, and the read is told again,
// not on every frame. Without Ghost's leave nothing changes.
func TestReadToTheEndHasNothingUnread(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	api := &ghostAPI{}
	s.history.api = api.client()
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.top[chat] = 12
	s.chats = []model.Chat{{ID: chat, Unread: 2, ReadInbox: 10}}
	unread := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.chats[0].Unread
	}

	s.MarkRead(chat, 12, false)
	if n := unread(); n != 2 {
		t.Fatalf("unread %d without Ghost's leave", n)
	}
	s.SetGhost(model.Ghost{SendRead: true})
	s.MarkRead(chat, 12, false)
	api.wait(t, 1)
	// Something brings the count back.
	s.setUnreadCount(chat, 3)
	for range 5 {
		s.MarkRead(chat, 12, false)
	}
	if n := unread(); n != 0 {
		t.Fatalf("unread %d at the end of the history", n)
	}
	if told := api.wait(t, 2); len(told) != 2 || told[1] != "read 12" {
		t.Fatalf("told %q, want the read told once more", told)
	}
	// Within readAgain it goes again, and is not told again.
	s.setUnreadCount(chat, 1)
	s.MarkRead(chat, 12, false)
	time.Sleep(50 * time.Millisecond)
	if n, told := unread(), api.wait(t, 2); n != 0 || len(told) != 2 {
		t.Fatalf("unread %d, told %q", n, told)
	}
}

// setUnreadCount sets the chat's count as a page of dialogs does.
func (s *Store) setUnreadCount(chat int64, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		if chats[i].ID == chat {
			chats[i].Unread = n
		}
	}
	s.chats = chats
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
