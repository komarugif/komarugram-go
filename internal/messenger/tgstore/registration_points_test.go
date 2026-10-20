// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// TestChatSignUp takes the point of a private chat whose last message is
// "joined Telegram", and of no other chat.
func TestChatSignUp(t *testing.T) {
	day := time.Date(2025, 8, 12, 0, 0, 0, 0, time.UTC)
	signUp := &model.Message{Kind: model.MessageService, Date: day, Service: &model.ServiceAction{Kind: model.ServiceContactSignUp}}
	other := &model.Message{Kind: model.MessageService, Date: day, Service: &model.ServiceAction{Kind: model.ServiceScreenshot}}
	for _, c := range []struct {
		chat model.Chat
		want bool
	}{
		{model.Chat{ID: 8262622416, Kind: model.KindUser, Last: model.Preview{Service: signUp}}, true},
		{model.Chat{ID: 8262622416, Kind: model.KindUser, Last: model.Preview{Service: other}}, false},
		{model.Chat{ID: 8262622416, Kind: model.KindUser, Last: model.Preview{Text: "hi"}}, false},
		{model.Chat{ID: -100, Kind: model.KindGroup, Last: model.Preview{Service: signUp}}, false},
	} {
		p, ok := chatSignUp(c.chat)
		if ok != c.want || ok && (p.User != c.chat.ID || !p.Date.Equal(day)) {
			t.Errorf("%+v: %+v %v", c.chat, p, ok)
		}
	}
}
