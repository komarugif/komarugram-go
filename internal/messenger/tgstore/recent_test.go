// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// TestRecentChatsKept checks that the search history outlives the store,
// in the account's cache.
func TestRecentChatsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	s := New(nil)
	if err := s.Configure(context.Background(), "a", path, nil); err != nil {
		t.Fatal(err)
	}
	s.BumpRecentChat(model.Chat{ID: 1, Title: "one"})
	s.BumpRecentChat(model.Chat{ID: 2, Title: "two", Unread: 4})
	s.BumpRecentChat(model.Chat{ID: 3, Title: "three"})
	s.RemoveRecentChat(1)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again := New(nil)
	if err := again.Configure(context.Background(), "a", path, nil); err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got := again.RecentChats()
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 2 || got[1].Unread != 0 {
		t.Fatalf("history after reopening: %+v", got)
	}
	again.ClearRecentChats()
	if len(again.RecentChats()) != 0 {
		t.Error("the history was not cleared")
	}
}

// A chat in the search history that has no dialog gets no updates of its
// status, so the history shows what the store heard of it since, from its
// profile or a history; one with a dialog is as the chat list has it.
func TestRecentChatsShowTheCurrentStatus(t *testing.T) {
	s := New(nil)
	s.BumpRecentChat(model.Chat{ID: 5, Kind: model.KindUser, Title: "Svatosh", Badges: model.Badges{User: 5, EmojiStatus: 7}})
	s.BumpRecentChat(model.Chat{ID: 6, Kind: model.KindUser, Title: "Unknown", Badges: model.Badges{User: 6, EmojiStatus: 9}})
	if got := s.RecentChats(); got[0].Badges.EmojiStatus != 9 || got[1].Badges.EmojiStatus != 7 {
		t.Fatalf("a peer not heard of changed: %+v", got)
	}
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 5, AccessHash: 1, FirstName: "Svatosh",
		EmojiStatus: &tg.EmojiStatus{DocumentID: 42}}}, nil)
	got := s.RecentChats()
	if got[1].ID != 5 || got[1].Badges.EmojiStatus != 42 {
		t.Fatalf("the status is %d, want the one heard of since, 42", got[1].Badges.EmojiStatus)
	}
	if got[0].Badges.EmojiStatus != 9 {
		t.Fatalf("another peer's status changed to %d", got[0].Badges.EmojiStatus)
	}
	if b, ok := s.PeerBadges(5); !ok || b.EmojiStatus != 42 {
		t.Fatalf("PeerBadges %+v %v", b, ok)
	}
	if _, ok := s.PeerBadges(6); ok {
		t.Fatal("badges of a peer not known")
	}
}
