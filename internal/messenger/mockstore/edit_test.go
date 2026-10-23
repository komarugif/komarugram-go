// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// The demo edits the account's own messages, as Telegram does: the history
// shows the text and the time of the edit, an edit to the same text is
// refused, and others' messages cannot be edited.
func TestDemoEditsOwnMessages(t *testing.T) {
	s := New(time.Now(), 0)
	ctx := context.Background()
	var own, others *model.Message
	for _, c := range s.Chats() {
		for _, m := range s.History(c.ID).Messages {
			m := m
			switch {
			case own == nil && m.Outgoing && m.Kind == model.MessageText && m.Text != "" && c.Kind == model.KindUser && model.Editable(m, c.Kind, time.Now(), s.EditTimeLimit()):
				own = &m
			case others == nil && !m.Outgoing && m.Kind == model.MessageText && c.Kind == model.KindUser:
				others = &m
			}
		}
	}
	if own == nil || others == nil {
		t.Fatal("the demo has no message of its own to edit, or none of others'")
	}
	entities := []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 3}}
	if err := s.EditMessage(ctx, own.Key, "New text", entities); err != nil {
		t.Fatal(err)
	}
	var got model.Message
	for _, m := range s.History(own.Key.ChatID).Messages {
		if m.Key == own.Key {
			got = m
		}
	}
	if got.Text != "New text" || got.EditedAt.IsZero() || len(got.Entities) != 1 || got.ContentRevision == own.ContentRevision {
		t.Fatalf("edit not shown: %+v", got)
	}
	if err := s.EditMessage(ctx, own.Key, "New text", entities); !errors.Is(err, model.ErrMessageNotModified) {
		t.Fatal("same text:", err)
	}
	if err := s.EditMessage(ctx, others.Key, "mine now", nil); !errors.Is(err, model.ErrEditForbidden) {
		t.Fatal("others' message:", err)
	}
}
