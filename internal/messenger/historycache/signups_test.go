// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// TestContactSignUps finds the private chats that begin with "joined
// Telegram": not one where it is not the first message cached, nor a
// group's.
func TestContactSignUps(t *testing.T) {
	ctx := context.Background()
	c, err := Open(filepath.Join(t.TempDir(), "h"), "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	day := time.Date(2025, 8, 12, 10, 30, 0, 0, time.UTC)
	message := func(chat int64, id int, signUp bool) model.Message {
		m := model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(id)}, Kind: model.MessageText, Text: "hi", Date: day}
		if signUp {
			m.Kind, m.Text, m.Service = model.MessageService, "", &model.ServiceAction{Kind: model.ServiceContactSignUp}
		}
		return m
	}
	err = c.SaveMessages(ctx, []model.Message{
		message(8262622416, 1, true), message(8262622416, 2, false),
		// The history cached does not begin with it.
		message(5000000001, 4, false), message(5000000001, 7, true),
		// A group's id is negative.
		message(-100, 1, true),
		message(5000000002, 3, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ContactSignUps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].User != 8262622416 || !got[0].Date.Equal(day) {
		t.Fatalf("got %+v", got)
	}
}
