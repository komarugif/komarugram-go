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

// waitSearch waits for the current search to finish loading.
func waitSearch(t *testing.T, s *Store) model.SearchResults {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r := s.SearchResults()
		if !r.Loading {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatal("the search did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSearchLocal(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	channel := peerID(&tg.PeerChannel{ChannelID: 8})
	s.chats = []model.Chat{
		{ID: 5, Kind: model.KindUser, Title: "Фёдор"},
		{ID: channel, Kind: model.KindChannel, Title: "Новости"},
	}
	_, err := s.ingest(ctx, []tg.MessageClass{
		&tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 5}, Message: "встреча в пятницу", Date: 10},
		&tg.Message{ID: 2, PeerID: &tg.PeerChannel{ChannelID: 8}, Message: "встреча отменена", Date: 20},
	}, false, 0)
	if err != nil {
		t.Fatal(err)
	}

	s.Search(model.SearchQuery{Text: "федор"})
	r := waitSearch(t, s)
	if len(r.Chats) != 1 || r.Chats[0].ID != 5 {
		t.Errorf("chats by name, with Ё as Е: %+v", r.Chats)
	}

	s.Search(model.SearchQuery{Text: "встреч"})
	r = waitSearch(t, s)
	if len(r.Messages) != 2 || r.Messages[0].Message.Key.MessageID != 2 || r.Messages[0].Chat.Title != "Новости" {
		t.Errorf("messages, newest first, with their chats: %+v", r.Messages)
	}

	s.Search(model.SearchQuery{Text: "встреч", Section: model.SearchChannels})
	r = waitSearch(t, s)
	if len(r.Messages) != 1 || r.Messages[0].Chat.ID != channel || len(r.Chats) != 0 {
		t.Errorf("channels only: %+v", r)
	}

	s.Search(model.SearchQuery{Text: "встреч", Global: true})
	if r = waitSearch(t, s); !errors.Is(r.Err, model.ErrSearchOffline) {
		t.Errorf("a global search without Telegram: %v", r.Err)
	}
}

func TestRevealDropsPagesOfTheOldHistory(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.history.histories[chat] = &model.History{}
	s.history.mu.Lock()
	epoch := s.history.epochs[chat]
	s.history.mu.Unlock()

	s.Reveal(chat, 40)
	s.history.histories[chat] = &model.History{}
	old := []model.Message{{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: 900}}}
	s.finishPageAt(epoch, chat, 0, old, true, nil)
	if len(s.History(chat).Messages) != 0 {
		t.Error("a page of the old history was merged into the revealed one")
	}
	if v, ok := s.Viewport(chat); !ok || v.AnchorMessageID != 40 || v.AtEnd {
		t.Errorf("the viewport does not start at the message: %+v", v)
	}
}

// contacts.search finds names like the query, in an order of its own, and
// not always the one it is: a channel it ranks out, or one of a user's
// several usernames. The chat whose username the query is comes first; one
// not found is resolved, once a session, and a name nobody has is not asked
// for again.
func TestSearchFindsTheUsername(t *testing.T) {
	s := testStore(t)
	var resolved []string
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch req := in.(type) {
		case *tg.ContactsSearchRequest:
			return &tg.ContactsFound{
				Results: []tg.PeerClass{&tg.PeerChannel{ChannelID: 11}, &tg.PeerUser{UserID: 12}},
				Chats:   []tg.ChatClass{&tg.Channel{ID: 11, AccessHash: 1, Title: "Panchan GIF chat", Username: "panchanchatgif", Photo: &tg.ChatPhotoEmpty{}}},
				Users:   []tg.UserClass{&tg.User{ID: 12, AccessHash: 2, FirstName: "Other", Username: "kdecat_fan"}},
			}, nil
		case *tg.ContactsResolveUsernameRequest:
			resolved = append(resolved, req.Username)
			switch req.Username {
			case "panchan_gif":
				return &tg.ContactsResolvedPeer{Peer: &tg.PeerChannel{ChannelID: 10},
					Chats: []tg.ChatClass{&tg.Channel{ID: 10, AccessHash: 3, Title: "Panchan ~ GIF", Username: "panchan_GIF", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}}}, nil
			case "kdecat":
				return &tg.ContactsResolvedPeer{Peer: &tg.PeerUser{UserID: 13},
					Users: []tg.UserClass{&tg.User{ID: 13, AccessHash: 4, FirstName: "SvatoshGPT", Usernames: []tg.Username{{Username: "svatosh", Active: true}, {Username: "kdecat", Active: true}}}}}, nil
			}
			return nil, &tgerr.Error{Code: 400, Type: "USERNAME_NOT_OCCUPIED"}
		}
		t.Fatalf("unexpected request %T", in)
		return nil, nil
	})
	search := func(text string, section model.SearchSection) []model.Chat {
		t.Helper()
		chats, err := s.searchContacts(context.Background(), s.history.api, model.SearchQuery{Text: text, Section: section, Global: true})
		if err != nil {
			t.Fatal(err)
		}
		return chats
	}
	if chats := search("panchan_gif", model.SearchChats); len(chats) != 3 || chats[0].ID != peerID(&tg.PeerChannel{ChannelID: 10}) {
		t.Fatalf("channel: %+v", chats)
	}
	if chats := search("@kdecat", model.SearchChats); len(chats) != 3 || chats[0].ID != 13 || chats[0].Title != "SvatoshGPT" {
		t.Fatalf("user: %+v", chats)
	}
	if chats := search("t.me/panchan_gif", model.SearchChannels); len(chats) != 1 || chats[0].Kind != model.KindChannel {
		t.Fatalf("channels: %+v", chats)
	}
	// Found by contacts.search, second: it comes first, and nothing more
	// is asked.
	if chats := search("@kdecat_fan", model.SearchChats); len(chats) != 2 || chats[0].ID != 12 || chats[1].ID != peerID(&tg.PeerChannel{ChannelID: 11}) {
		t.Fatalf("found second: %+v", chats)
	}
	// Not a username: nothing more is asked.
	search("pan", model.SearchChats)
	// Nobody has it: asked once.
	search("nobody_here", model.SearchChats)
	search("nobody_here", model.SearchChats)
	if want := []string{"panchan_gif", "kdecat", "nobody_here"}; len(resolved) != len(want) || resolved[0] != want[0] || resolved[1] != want[1] || resolved[2] != want[2] {
		t.Fatalf("resolved %q", resolved)
	}
}
