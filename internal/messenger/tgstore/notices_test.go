// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A notice goes out once for each new incoming message, with the chat it
// came to; not for one sent, edited, old, of a channel the account is not
// in, or a service message.
func TestNoticesOfNewIncomingMessages(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	group := peerID(&tg.PeerChat{ChatID: 7})
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Name: "Alice"}
	s.history.peers[group] = peerRecord{Kind: "chat", ID: 7, Name: "Team"}
	channel := peerID(&tg.PeerChannel{ChannelID: 9})
	s.history.peers[channel] = peerRecord{Kind: "channel", ID: 9, Name: "News", Rights: peerRights{Left: true, Broadcast: true}}
	s.chats = []model.Chat{{ID: 5, Kind: model.KindUser, Title: "Alice"}, {ID: group, Kind: model.KindGroup, Title: "Team"}}
	var got []model.MessageNotice
	s.SetNotices(func(n model.MessageNotice) { got = append(got, n) })
	now := int(time.Now().Unix())
	handle := func(u tg.UpdateClass) {
		t.Helper()
		if err := s.Handle(ctx, &tg.UpdateShort{Update: u}); err != nil {
			t.Fatal(err)
		}
	}
	hello := &tg.Message{ID: 10, PeerID: &tg.PeerUser{UserID: 5}, FromID: &tg.PeerUser{UserID: 5}, Message: "hello", Date: now}
	handle(&tg.UpdateNewMessage{Message: hello})
	handle(&tg.UpdateNewMessage{Message: hello})
	handle(&tg.UpdateEditMessage{Message: &tg.Message{ID: 10, PeerID: &tg.PeerUser{UserID: 5}, Message: "hello!", Date: now, EditDate: now}})
	handle(&tg.UpdateNewMessage{Message: &tg.Message{ID: 11, Out: true, PeerID: &tg.PeerUser{UserID: 5}, Message: "mine", Date: now}})
	handle(&tg.UpdateNewMessage{Message: &tg.Message{ID: 12, PeerID: &tg.PeerUser{UserID: 5}, Message: "old", Date: now - 3600}})
	handle(&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 3, PeerID: &tg.PeerChannel{ChannelID: 9}, Message: "post", Date: now}})
	handle(&tg.UpdateNewMessage{Message: &tg.MessageService{ID: 13, PeerID: &tg.PeerChat{ChatID: 7}, FromID: &tg.PeerUser{UserID: 5}, Action: &tg.MessageActionChatJoinedByLink{}, Date: now}})
	silent := &tg.Message{ID: 14, PeerID: &tg.PeerChat{ChatID: 7}, FromID: &tg.PeerUser{UserID: 5}, Message: "quiet", Date: now}
	silent.SetSilent(true)
	handle(&tg.UpdateNewMessage{Message: silent})
	if len(got) != 2 {
		t.Fatalf("%d notices: %+v", len(got), got)
	}
	if n := got[0]; n.Chat.ID != 5 || n.Chat.Title != "Alice" || n.Message.Text != "hello" || n.Message.Sender != "" || n.Silent {
		t.Errorf("private: %+v", n)
	}
	if n := got[1]; n.Chat.ID != group || n.Message.Sender != "Alice" || n.Message.Text != "quiet" || !n.Silent {
		t.Errorf("group: %+v", n)
	}

	// A chat muted elsewhere is muted at once.
	until := &tg.PeerNotifySettings{}
	until.SetMuteUntil(int(time.Now().Add(time.Hour).Unix()))
	handle(&tg.UpdateNotifySettings{Peer: &tg.NotifyPeer{Peer: &tg.PeerUser{UserID: 5}}, NotifySettings: *until})
	handle(&tg.UpdateNewMessage{Message: &tg.Message{ID: 15, PeerID: &tg.PeerUser{UserID: 5}, Message: "again", Date: now}})
	if n := got[len(got)-1]; n.Message.Text != "again" || !n.Chat.Muted {
		t.Errorf("after muting: %+v", n)
	}
}
