// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

func TestResolveUsername(t *testing.T) {
	s := testStore(t)
	calls := 0
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.ContactsResolveUsernameRequest)
		if !ok {
			return nil
		}
		calls++
		if req.Username != "gochannel" {
			return &tgerr.Error{Code: 400, Type: "USERNAME_NOT_OCCUPIED"}
		}
		*out.(*tg.ContactsResolvedPeer) = tg.ContactsResolvedPeer{
			Peer:  &tg.PeerChannel{ChannelID: 9},
			Chats: []tg.ChatClass{&tg.Channel{ID: 9, AccessHash: 3, Title: "Go", Username: "gochannel", Broadcast: true}},
		}
		return nil
	}))
	ctx := context.Background()
	chat, err := s.ResolveUsername(ctx, "gochannel")
	if err != nil || chat.ID != peerID(&tg.PeerChannel{ChannelID: 9}) {
		t.Fatalf("chat %+v, %v", chat, err)
	}
	// Known now: no second request.
	if _, err := s.ResolveUsername(ctx, "GoChannel"); err != nil || calls != 1 {
		t.Fatalf("%v, %d calls", err, calls)
	}
	if _, err := s.ResolveUsername(ctx, "nobody"); !errors.Is(err, model.ErrLinkNotFound) {
		t.Fatalf("unknown name: %v", err)
	}
}

// A mention without a username links to its user by ID.
func TestMentionNameLinksTheUser(t *testing.T) {
	s := testStore(t)
	msgs, err := s.ingest(context.Background(), []tg.MessageClass{&tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 5}, Message: "hi Pavel", Date: 10,
		Entities: []tg.MessageEntityClass{&tg.MessageEntityMentionName{Offset: 3, Length: 5, UserID: 42}}}}, false, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatal(err)
	}
	runs := model.TextRuns(msgs[0].Text, msgs[0].Entities)
	if len(runs) != 2 || runs[1].URL != "tg://user?id=42" {
		t.Fatalf("runs %+v", runs)
	}
}

// A supergroup's member known only as a min user is asked for by the
// message that names them.
func TestSenderChatFromMessage(t *testing.T) {
	s := testStore(t)
	s.rememberPeers(
		[]tg.UserClass{&tg.User{ID: 42, Min: true, FirstName: "Pavel"}, &tg.User{ID: 43, AccessHash: 5, FirstName: "Known"}},
		[]tg.ChatClass{&tg.Channel{ID: 9, AccessHash: 3, Title: "Group", Megagroup: true}},
	)
	group := peerID(&tg.PeerChannel{ChannelID: 9})
	var asked *tg.InputUserFromMessage
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		req, ok := in.(*tg.UsersGetUsersRequest)
		if !ok {
			t.Fatalf("unexpected request %T", in)
		}
		asked, _ = req.ID[0].(*tg.InputUserFromMessage)
		if asked == nil || asked.UserID != 42 {
			return &tg.UserClassVector{Elems: []tg.UserClass{&tg.UserEmpty{ID: 1}}}, nil
		}
		return &tg.UserClassVector{Elems: []tg.UserClass{&tg.User{ID: 42, AccessHash: 7, FirstName: "Pavel"}}}, nil
	})
	ctx := context.Background()
	message := func(sender int64) model.Message {
		return model.Message{Key: model.MessageKey{ChatID: group, MessageID: 100}, SenderID: sender}
	}
	if chat, err := s.SenderChat(ctx, message(43)); err != nil || chat.ID != 43 || asked != nil {
		t.Fatalf("known sender: %+v %v, asked %+v", chat, err, asked)
	}
	chat, err := s.SenderChat(ctx, message(42))
	if err != nil || chat.ID != 42 || chat.Title != "Pavel" {
		t.Fatalf("min sender: %+v %v", chat, err)
	}
	if p, ok := asked.Peer.(*tg.InputPeerChannel); !ok || p.ChannelID != 9 || p.AccessHash != 3 || asked.MsgID != 100 {
		t.Fatalf("asked %+v", asked)
	}
	// Known now, with the access hash Telegram gave.
	if _, err := s.UserByID(ctx, 42); err != nil {
		t.Fatalf("not remembered: %v", err)
	}
	if _, err := s.SenderChat(ctx, message(44)); !errors.Is(err, model.ErrLinkNotFound) {
		t.Fatalf("unknown sender: %v", err)
	}
	if _, err := s.SenderChat(ctx, message(-1009)); !errors.Is(err, model.ErrLinkNotFound) {
		t.Fatalf("channel sender: %v", err)
	}
}

// A channel that posted in a group opens whether the account follows it
// or not, as Telegram finds it by the message; one the account is banned
// from opens on what the cache has of it, and is inaccessible only with
// nothing cached.
func TestSenderChannel(t *testing.T) {
	s := testStore(t)
	s.rememberPeers(nil, []tg.ChatClass{
		&tg.Channel{ID: 9, AccessHash: 3, Title: "Group", Megagroup: true},
		&tg.Channel{ID: 20, Min: true, Title: "Open", Broadcast: true},
		&tg.Channel{ID: 21, Min: true, Title: "Banned", Broadcast: true},
		&tg.Channel{ID: 22, Min: true, Title: "Cached", Broadcast: true},
	})
	group := peerID(&tg.PeerChannel{ChannelID: 9})
	channel := func(id int64) int64 { return peerID(&tg.PeerChannel{ChannelID: id}) }
	var asked []tg.InputChannelClass
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		req, ok := in.(*tg.ChannelsGetChannelsRequest)
		if !ok {
			t.Fatalf("unexpected request %T", in)
		}
		asked = append(asked, req.ID[0])
		from, ok := req.ID[0].(*tg.InputChannelFromMessage)
		if !ok {
			return nil, &tgerr.Error{Code: 400, Type: "CHANNEL_INVALID"}
		}
		if from.ChannelID == 20 {
			return &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Channel{ID: 20, AccessHash: 7, Title: "Open", Broadcast: true, Left: true}}}, nil
		}
		return &tg.MessagesChats{Chats: []tg.ChatClass{&tg.ChannelForbidden{ID: from.ChannelID, AccessHash: 8, Title: "Gone", Broadcast: true}}}, nil
	})
	ctx := context.Background()
	message := func(sender int64) model.Message {
		return model.Message{Key: model.MessageKey{ChatID: group, MessageID: 100}, SenderID: sender, SenderName: "Name"}
	}
	chat, err := s.SenderChat(ctx, message(channel(20)))
	if err != nil || chat.ID != channel(20) || chat.Kind != model.KindChannel {
		t.Fatalf("open channel: %+v %v", chat, err)
	}
	if from, ok := asked[0].(*tg.InputChannelFromMessage); !ok || from.MsgID != 100 || from.Peer.(*tg.InputPeerChannel).ChannelID != 9 {
		t.Fatalf("asked %+v", asked[0])
	}
	chat, err = s.SenderChat(ctx, message(channel(21)))
	if !errors.Is(err, model.ErrChatInaccessible) || chat.ID != channel(21) || chat.Title != "Gone" {
		t.Fatalf("banned channel: %+v %v", chat, err)
	}
	if err := s.history.cache.SaveMessages(ctx, []model.Message{{Key: model.MessageKey{AccountID: "a", ChatID: channel(22), MessageID: 5}, Text: "kept", ContentRevision: 1}}); err != nil {
		t.Fatal(err)
	}
	if chat, err := s.SenderChat(ctx, message(channel(22))); err != nil || chat.ID != channel(22) {
		t.Fatalf("banned channel with its cache: %+v %v", chat, err)
	}
	// The group's anonymous admins post as the group itself.
	n := len(asked)
	if _, err := s.SenderChat(ctx, message(group)); !errors.Is(err, model.ErrLinkNotFound) || len(asked) != n {
		t.Fatalf("anonymous admin: %v, %d requests", err, len(asked)-n)
	}
	// Offline, a channel known opens as it is.
	s.history.api = nil
	if chat, err := s.SenderChat(ctx, message(channel(20))); err != nil || chat.ID != channel(20) {
		t.Fatalf("offline: %+v %v", chat, err)
	}
}
