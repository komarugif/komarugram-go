// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

func pinnedIDs(s *Store) (ids []int64) {
	for _, c := range s.Chats() {
		if c.Pinned {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func chatOrder(s *Store) (ids []int64) {
	for _, c := range s.Chats() {
		ids = append(ids, c.ID)
	}
	return ids
}

func pinStore(t *testing.T) *Store {
	s := testStore(t)
	now := time.Unix(1000, 0)
	s.chats = []model.Chat{
		{ID: 1, Pinned: true, PinRank: 1, LastTime: now.Add(-3 * time.Hour)},
		{ID: 2, Pinned: true, PinRank: 2, LastTime: now.Add(-4 * time.Hour)},
		{ID: 3, LastTime: now.Add(-1 * time.Minute)},
		{ID: 4, LastTime: now.Add(-2 * time.Minute)},
	}
	for id := int64(1); id <= 4; id++ {
		s.history.peers[id] = peerRecord{Kind: "user", ID: id, Hash: id * 11}
	}
	return s
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Pinning asks Telegram, and only when it agrees the chat goes first among
// the pinned; a limit that is reached is told as such and changes nothing.
func TestPinChat(t *testing.T) {
	s := pinStore(t)
	var asked []*tg.MessagesToggleDialogPinRequest
	var fail error
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesToggleDialogPinRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		asked = append(asked, r)
		if fail != nil {
			return nil, fail
		}
		return &tg.BoolTrue{}, nil
	})
	ctx := context.Background()

	if err := s.PinChat(ctx, 4, true); err != nil {
		t.Fatal(err)
	}
	peer, ok := asked[0].Peer.(*tg.InputDialogPeer)
	if !ok || !asked[0].Pinned {
		t.Fatalf("asked %+v", asked[0])
	}
	if user, ok := peer.Peer.(*tg.InputPeerUser); !ok || user.UserID != 4 || user.AccessHash != 44 {
		t.Fatalf("asked to pin %+v", peer.Peer)
	}
	if got := chatOrder(s); !equal(got, []int64{4, 1, 2, 3}) {
		t.Fatalf("order %v after pinning 4, want the new pin first", got)
	}

	fail = tgerr.New(400, "PINNED_DIALOGS_TOO_MUCH")
	if err := s.PinChat(ctx, 3, true); !errors.Is(err, model.ErrPinnedTooMuch) {
		t.Fatalf("pinning over the limit: %v", err)
	}
	if got := pinnedIDs(s); !equal(got, []int64{4, 1, 2}) {
		t.Fatalf("pinned %v after a refused pin", got)
	}

	fail = nil
	if err := s.PinChat(ctx, 1, false); err != nil {
		t.Fatal(err)
	}
	if got := chatOrder(s); !equal(got, []int64{4, 2, 3, 1}) {
		t.Fatalf("order %v after unpinning 1: it goes by its last message", got)
	}
	if got := s.Chats(); got[1].PinRank != 2 || got[0].PinRank != 1 {
		t.Fatalf("ranks %+v", got)
	}
}

// A new order of the pinned chats is the list's at once, and Telegram is
// told it, forced, for the main list; one it refuses goes, and one of chats
// that are not all the pinned ones is not asked.
func TestReorderPinned(t *testing.T) {
	s := pinStore(t)
	s.chats = model.WithPinned(s.chats, 3, true) // 3, 1, 2 pinned.
	var asked []*tg.MessagesReorderPinnedDialogsRequest
	var fail error
	var during []int64
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesReorderPinnedDialogsRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		asked = append(asked, r)
		during = pinnedIDs(s)
		if fail != nil {
			return nil, fail
		}
		return &tg.BoolTrue{}, nil
	})
	ctx := context.Background()

	if err := s.ReorderPinned(ctx, []int64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || !asked[0].Force || asked[0].FolderID != 0 || len(asked[0].Order) != 3 {
		t.Fatalf("asked %+v", asked)
	}
	var told []int64
	for _, p := range asked[0].Order {
		user := p.(*tg.InputDialogPeer).Peer.(*tg.InputPeerUser)
		if user.AccessHash != user.UserID*11 {
			t.Fatalf("peer %+v", user)
		}
		told = append(told, user.UserID)
	}
	if !equal(told, []int64{1, 2, 3}) {
		t.Fatalf("told the order %v", told)
	}
	if !equal(during, []int64{1, 2, 3}) {
		t.Fatalf("the list had %v before Telegram answered", during)
	}
	if got := chatOrder(s); !equal(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("order %v", got)
	}

	fail = errors.New("boom")
	if err := s.ReorderPinned(ctx, []int64{2, 3, 1}); err == nil {
		t.Fatal("a refused order came to no error")
	}
	if got := pinnedIDs(s); !equal(got, []int64{1, 2, 3}) {
		t.Fatalf("pinned %v after a refused order, want the one before", got)
	}

	fail = nil
	if err := s.ReorderPinned(ctx, []int64{2, 1}); !errors.Is(err, model.ErrPinnedChanged) {
		t.Fatalf("an order without a pinned chat: %v", err)
	}
	if err := s.ReorderPinned(ctx, []int64{2, 1, 4}); !errors.Is(err, model.ErrPinnedChanged) {
		t.Fatalf("an order with a chat not pinned: %v", err)
	}
	if len(asked) != 2 {
		t.Fatalf("asked %d times, want orders of other chats not asked", len(asked))
	}
}

// Pins made elsewhere come in updates: one chat, or the whole order.
func TestPinUpdates(t *testing.T) {
	s := pinStore(t)
	ctx := context.Background()
	if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateDialogPinned{Pinned: true, Peer: &tg.DialogPeer{Peer: &tg.PeerUser{UserID: 3}}}}); err != nil {
		t.Fatal(err)
	}
	if got := chatOrder(s); !equal(got, []int64{3, 1, 2, 4}) {
		t.Fatalf("order %v after 3 was pinned", got)
	}
	order := &tg.UpdatePinnedDialogs{}
	order.SetOrder([]tg.DialogPeerClass{&tg.DialogPeer{Peer: &tg.PeerUser{UserID: 2}}, &tg.DialogPeer{Peer: &tg.PeerUser{UserID: 3}}})
	if err := s.Handle(ctx, &tg.UpdateShort{Update: order}); err != nil {
		t.Fatal(err)
	}
	if got := chatOrder(s); !equal(got, []int64{2, 3, 4, 1}) {
		t.Fatalf("order %v after the pinned were reordered, and 1 unpinned", got)
	}
	// A pin in another folder is not the list's.
	other := &tg.UpdateDialogPinned{Pinned: true, Peer: &tg.DialogPeer{Peer: &tg.PeerUser{UserID: 4}}}
	other.SetFolderID(1)
	if err := s.Handle(ctx, &tg.UpdateShort{Update: other}); err != nil {
		t.Fatal(err)
	}
	if got := pinnedIDs(s); !equal(got, []int64{2, 3}) {
		t.Fatalf("pinned %v after a pin in the archive", got)
	}
}

// A new message moves a chat among the unpinned, never among the pinned,
// which keep the order they were pinned in.
func TestPinnedKeepTheirOrderOnNewMessages(t *testing.T) {
	s := pinStore(t)
	ctx := context.Background()
	msg := &tg.Message{ID: 90, PeerID: &tg.PeerUser{UserID: 2}, Message: "new", Date: 2000}
	if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateNewMessage{Message: msg}}); err != nil {
		t.Fatal(err)
	}
	if got := chatOrder(s); !equal(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("order %v after a message to the second pinned chat", got)
	}
}

// Marking a chat read tells Telegram up to its last message, without Ghost's
// leave, as the chat menu asks.
func TestMarkChatRead(t *testing.T) {
	s := pinStore(t)
	s.history.top[3] = 42
	api := &ghostAPI{}
	s.history.api = api.client()
	s.MarkChatRead(3)
	if told := api.wait(t, 1); told[0] != "read 42" {
		t.Fatalf("told %q", told)
	}
}
