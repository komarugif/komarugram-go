// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"komarugram/internal/messenger/model"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"testing"
)

func TestStickerSetAuthor(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 77, AccessHash: 8, FirstName: "Known"}}, nil)
	if chat, err := s.StickerSetAuthor(context.Background(), 77); err != nil || chat.ID != 77 || chat.Title != "Known" {
		t.Fatalf("cached author: %+v %v", chat, err)
	}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		req, ok := in.(*tg.UsersGetUsersRequest)
		if !ok {
			t.Fatalf("unexpected request %T", in)
		}
		user := req.ID[0].(*tg.InputUser)
		if user.UserID == 88 {
			return &tg.UserClassVector{Elems: []tg.UserClass{&tg.User{ID: 88, AccessHash: 9, FirstName: "Creator"}}}, nil
		}
		return &tg.UserClassVector{Elems: []tg.UserClass{&tg.UserEmpty{ID: user.UserID}}}, nil
	})
	if chat, err := s.StickerSetAuthor(context.Background(), 88); err != nil || chat.ID != 88 || chat.Title != "Creator" {
		t.Fatalf("resolved author: %+v %v", chat, err)
	}
	if _, err := s.StickerSetAuthor(context.Background(), 99); err == nil {
		t.Fatal("inaccessible author was accepted")
	}
}

// An author known only by id is asked for by a message of theirs the cache
// has in a group, as Telegram gives a group's member brought as a min one.
func TestStickerSetAuthorFromMessage(t *testing.T) {
	s := testStore(t)
	s.rememberPeers(nil, []tg.ChatClass{&tg.Channel{ID: 9, AccessHash: 3, Title: "Group", Megagroup: true}})
	group := peerID(&tg.PeerChannel{ChannelID: 9})
	ctx := context.Background()
	if err := s.history.cache.SaveMessages(ctx, []model.Message{{Key: model.MessageKey{AccountID: s.history.account, ChatID: group, MessageID: 100}, SenderID: 55, Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	var asked *tg.InputUserFromMessage
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		req, ok := in.(*tg.UsersGetUsersRequest)
		if !ok {
			t.Fatalf("unexpected request %T", in)
		}
		if asked, _ = req.ID[0].(*tg.InputUserFromMessage); asked != nil && asked.UserID == 55 {
			return &tg.UserClassVector{Elems: []tg.UserClass{&tg.User{ID: 55, AccessHash: 6, FirstName: "Author"}}}, nil
		}
		return &tg.UserClassVector{Elems: []tg.UserClass{&tg.UserEmpty{ID: 1}}}, nil
	})
	chat, err := s.StickerSetAuthor(ctx, 55)
	if err != nil || chat.ID != 55 || chat.Title != "Author" {
		t.Fatalf("author: %+v %v", chat, err)
	}
	if asked == nil || asked.MsgID != 100 {
		t.Fatalf("asked %+v", asked)
	}
}
