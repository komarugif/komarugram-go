// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A service message keeps who did what, and the chat list words it.
func TestServiceMessage(t *testing.T) {
	now := int(time.Now().Unix())
	added := &tg.MessageService{ID: 50, PeerID: &tg.PeerChat{ChatID: 5}, Date: now, Action: &tg.MessageActionChatAddUser{Users: []int64{3}}}
	added.SetFromID(&tg.PeerUser{UserID: 2})
	page := testPage()
	page.Messages[4] = added
	l := newList(self)
	l.add(page)
	chats, _ := l.snapshot(nil)
	if got, want := chatText(chats[4]), "Anna S добавил(а) Stranger"; got != want {
		t.Errorf("the group's last message reads %q, want %q", got, want)
	}
	if chatSender(chats[4]) != "" {
		t.Errorf("an action names its sender itself, not as %q", chatSender(chats[4]))
	}

	m, _ := convertMessage("a", added, map[int64]string{2: "Anna", 3: "Bob"})
	want := model.ServiceAction{Kind: model.ServiceAddUser, Peers: []model.ServicePeer{{ID: 3, Name: "Bob"}}}
	if m.Kind != model.MessageService || m.SenderID != 2 || m.SenderName != "Anna" || m.Service == nil || m.Service.Kind != want.Kind || len(m.Service.Peers) != 1 || m.Service.Peers[0] != want.Peers[0] {
		t.Fatalf("converted to %+v, service %+v", m, m.Service)
	}

	pin := &tg.MessageService{ID: 51, PeerID: &tg.PeerChat{ChatID: 5}, Date: now, Action: &tg.MessageActionPinMessage{}, ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 20}}
	m, _ = convertMessage("a", pin, nil)
	if m.Service == nil || m.Service.Kind != model.ServicePin || m.ReplyToMessageID != 20 {
		t.Fatalf("a pin converted to %+v", m)
	}
	chat := model.Chat{Kind: model.KindGroup, Title: "Group"}
	setPreview(&chat, m)
	if chatText(chat) != "Group закрепил(а) сообщение" {
		t.Errorf("a pin reads %q in the list", chatText(chat))
	}

	call := &tg.MessageService{ID: 52, Out: true, PeerID: &tg.PeerUser{UserID: 2}, Date: now, Action: &tg.MessageActionPhoneCall{Video: true, Reason: &tg.PhoneCallDiscardReasonMissed{}}}
	m, _ = convertMessage("a", call, nil)
	if m.ServicePill() || !m.Outgoing || m.Service.Reason != "missed" || !m.Service.Video {
		t.Fatalf("a call converted to %+v %+v", m, m.Service)
	}
}
