// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
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
		if _, ok := in.(*tg.ContactsResolveUsernameRequest); ok {
			// No lookup bot: the test of it is TestStickerSetAuthorFromBot.
			return nil, errors.New("USERNAME_NOT_OCCUPIED")
		}
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

// An author no message is cached of is asked of the lookup bot inline: a
// user it mentions comes from Telegram with an access hash; a bot it names
// by its username, which is resolved and taken when it is the author. A
// username that is not the bot's own ID's is not asked.
func TestStickerSetAuthorFromBot(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		botID   int64
		author  int64
		wantErr bool
	}{
		{"user", lookupBotID, 61, false},
		{"bot", lookupBotID, 62, false},
		{"unknown", lookupBotID, 63, true},
		{"another bot", 1234, 61, true},
	} {
		s := testStore(t)
		queried := false
		s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
			switch req := in.(type) {
			case *tg.ContactsResolveUsernameRequest:
				switch req.Username {
				case lookupBotUsername:
					return &tg.ContactsResolvedPeer{Peer: &tg.PeerUser{UserID: c.botID}, Users: []tg.UserClass{&tg.User{ID: c.botID, AccessHash: 4, Bot: true, Username: lookupBotUsername}}}, nil
				case "AuthorBot":
					return &tg.ContactsResolvedPeer{Peer: &tg.PeerUser{UserID: 62}, Users: []tg.UserClass{&tg.User{ID: 62, AccessHash: 5, Bot: true, FirstName: "Author Bot"}}}, nil
				}
				return nil, errors.New("USERNAME_NOT_OCCUPIED")
			case *tg.MessagesGetInlineBotResultsRequest:
				queried = true
				if bot, ok := req.Bot.(*tg.InputUser); !ok || bot.UserID != lookupBotID || bot.AccessHash != 4 {
					t.Fatalf("%s: asked %+v", c.name, req.Bot)
				}
				text := func(s string) []tg.BotInlineResultClass {
					return []tg.BotInlineResultClass{&tg.BotInlineResult{ID: "001", Type: "article", SendMessage: &tg.BotInlineMessageText{Message: s}}}
				}
				switch req.Query {
				case "61":
					return &tg.MessagesBotResults{Results: text("├ user_id: 61\n├ name: Author"), Users: []tg.UserClass{&tg.User{ID: 61, AccessHash: 6, FirstName: "Author"}}}, nil
				case "62":
					return &tg.MessagesBotResults{Results: text("├ id: 62\n├ name: Author Bot\n├ username: @AuthorBot")}, nil
				}
				return &tg.MessagesBotResults{Results: text("An empty request was sent")}, nil
			case *tg.UsersGetUsersRequest:
				return &tg.UserClassVector{Elems: []tg.UserClass{&tg.UserEmpty{ID: 1}}}, nil
			}
			t.Fatalf("%s: unexpected request %T", c.name, in)
			return nil, nil
		})
		chat, err := s.StickerSetAuthor(ctx, c.author)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: opened %+v", c.name, chat)
			}
			if c.botID != lookupBotID && queried {
				t.Errorf("%s: the bot of another ID was asked", c.name)
			}
			continue
		}
		if err != nil || chat.ID != c.author {
			t.Errorf("%s: %+v, %v", c.name, chat, err)
		}
	}
}
