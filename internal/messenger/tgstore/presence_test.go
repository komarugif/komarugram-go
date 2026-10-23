// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// Telegram tells when users were online with the users it sends and in
// updateUserStatus; the latest is kept.
func TestPresenceFromUsersAndUpdates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, ok := s.Presence(5); ok {
		t.Fatal("a presence before Telegram told one")
	}
	err := s.Handle(ctx, &tg.Updates{Users: []tg.UserClass{&tg.User{ID: 5, AccessHash: 1, Status: &tg.UserStatusOffline{WasOnline: 1000}}}})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := s.Presence(5); !ok || p.Seen != model.SeenAt || !p.At.Equal(time.Unix(1000, 0)) || p.Online(time.Unix(1001, 0)) {
		t.Fatalf("from the user: %+v %v", p, ok)
	}
	if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateUserStatus{UserID: 5, Status: &tg.UserStatusOnline{Expires: 2000}}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Presence(5); !p.Online(time.Unix(1500, 0)) || p.Online(time.Unix(2000, 0)) {
		t.Fatalf("from the update: %+v", p)
	}
	if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateUserStatus{UserID: 5, Status: &tg.UserStatusLastWeek{}}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Presence(5); p.Seen != model.SeenLastWeek {
		t.Fatalf("hidden: %+v", p)
	}
}

// How far the other side read the account's messages comes with the
// dialog, and goes on with updates, never back.
func TestReadOutbox(t *testing.T) {
	l := newList(1)
	l.messages[[2]int64{5, 4}] = &tg.Message{ID: 4, Out: true, PeerID: &tg.PeerUser{UserID: 5}, Message: "hi", Date: 10}
	chat, _ := l.chat(&tg.Dialog{Peer: &tg.PeerUser{UserID: 5}, TopMessage: 4, ReadOutboxMaxID: 3})
	if chat.ReadOutbox != 3 || !chat.Last.Sent || chat.Last.ID != 4 || chat.LastSendState() != model.SendSent {
		t.Fatalf("from the dialog: %+v", chat)
	}

	s := testStore(t)
	s.chats = []model.Chat{chat}
	ctx := context.Background()
	if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateReadHistoryOutbox{Peer: &tg.PeerUser{UserID: 5}, MaxID: 4}}); err != nil {
		t.Fatal(err)
	}
	if got := s.Chats()[0]; got.ReadOutbox != 4 || got.LastSendState() != model.SendRead {
		t.Fatalf("after the update: %+v", got)
	}
	if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateReadHistoryOutbox{Peer: &tg.PeerUser{UserID: 5}, MaxID: 2}}); err != nil {
		t.Fatal(err)
	}
	if got := s.Chats()[0].ReadOutbox; got != 4 {
		t.Fatalf("went back to %d", got)
	}
}

// A text message shows, on its way, at the end of its history, and the
// message Telegram makes of it takes its place; one that fails goes.
func TestSendShowsMessageOnItsWay(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.histories[5] = &model.History{}
	fail := false
	var during model.History
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch in.(type) {
		case *tg.MessagesSendMessageRequest:
			during, _ = s.HistorySince(5, 0)
			if fail {
				return nil, errors.New("flood")
			}
			return &tg.UpdateShortSentMessage{ID: 9, Date: 10}, nil
		case *tg.MessagesGetMessagesRequest, *tg.MessagesReadHistoryRequest:
			return nil, errors.New("offline")
		default:
			return nil, fmt.Errorf("unexpected %T", in)
		}
	})
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(during.Messages) != 1 || !during.Messages[0].Sending || during.Messages[0].Text != "hi" || during.Messages[0].Key.MessageID >= 0 {
		t.Fatalf("on its way: %+v", during.Messages)
	}
	if model.MessageSendState(during.Messages[0], model.KindUser, 100) != model.SendSending {
		t.Fatal("no clock on its way")
	}
	after, _ := s.HistorySince(5, 0)
	if len(after.Messages) != 1 || after.Messages[0].Sending || after.Messages[0].Key.MessageID != 9 {
		t.Fatalf("once taken: %+v", after.Messages)
	}

	fail = true
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 2, Text: "again"}); err == nil {
		t.Fatal("the failure was not told")
	}
	if len(during.Messages) != 2 || !during.Messages[1].Sending {
		t.Fatalf("the second on its way: %+v", during.Messages)
	}
	if after, _ := s.HistorySince(5, 0); len(after.Messages) != 1 {
		t.Fatalf("a failed message stays: %+v", after.Messages)
	}
}

// The message on its way stands in for the one Telegram made of it until
// the history has that: never both at once, never neither.
func TestPendingGivesWayToItsMessage(t *testing.T) {
	c := newConversation()
	c.histories[5] = &model.History{}
	c.pending.list = []*pendingMessage{{chat: 5, seq: 1, msg: model.Message{Sending: true}}}
	if got := c.pendingMessagesOf(5, c.histories[5].Messages); len(got) != 1 {
		t.Fatal("not shown on its way")
	}
	c.sentPending(c.pending.list[0], 9)
	if got := c.pendingMessagesOf(5, nil); len(got) != 1 {
		t.Fatal("gone before its message is in the history")
	}
	if got := c.pendingMessagesOf(5, []model.Message{{Key: model.MessageKey{MessageID: 8}}, {Key: model.MessageKey{MessageID: 9}}}); len(got) != 0 {
		t.Fatal("shown beside its message")
	}
	if got := c.pendingMessagesOf(6, nil); len(got) != 0 {
		t.Fatal("shown in another chat")
	}
}
