// SPDX-License-Identifier: Unlicense OR MIT

package model

import "testing"

// The marks are Telegram Desktop's: none for what the account did not
// send and for a channel's posts, the clock on its way, two checks once
// read, and in Saved Messages and to bots at once.
func TestMessageSendState(t *testing.T) {
	out := func(id MessageID) Message { return Message{Key: MessageKey{MessageID: id}, Outgoing: true} }
	for _, c := range []struct {
		name string
		m    Message
		kind ChatKind
		read MessageID
		want SendState
	}{
		{"incoming", Message{Key: MessageKey{MessageID: 3}}, KindUser, 10, SendNone},
		{"read", out(3), KindUser, 3, SendRead},
		{"unread", out(4), KindUser, 3, SendSent},
		{"group", out(4), KindGroup, 5, SendRead},
		{"bot", out(4), KindBot, 0, SendRead},
		{"saved", out(4), KindSaved, 0, SendRead},
		{"post", Message{Key: MessageKey{MessageID: 4}, Outgoing: true, Post: true}, KindChannel, 0, SendNone},
		{"service", Message{Key: MessageKey{MessageID: 4}, Outgoing: true, Kind: MessageService}, KindUser, 0, SendNone},
		{"sending", Message{Key: MessageKey{MessageID: -5}, Outgoing: true, Sending: true}, KindUser, 100, SendSending},
	} {
		if got := MessageSendState(c.m, c.kind, c.read); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	chat := Chat{Kind: KindUser, Last: Preview{ID: 7, Sent: true}, ReadOutbox: 6}
	if got := chat.LastSendState(); got != SendSent {
		t.Errorf("list, unread: %v", got)
	}
	chat.ReadOutbox = 7
	if got := chat.LastSendState(); got != SendRead {
		t.Errorf("list, read: %v", got)
	}
	chat.Last.Sent = false
	if got := chat.LastSendState(); got != SendNone {
		t.Errorf("list, incoming: %v", got)
	}
}
