// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"time"

	"komarugram/internal/messenger/model"
)

// EditMessage edits a message of the demo as Telegram would: the account's
// own, within the edit time limit, with a text that changed.
func (s *Store) EditMessage(ctx context.Context, key model.MessageKey, text string, entities []model.Entity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.History(key.ChatID)
	s.mu.Lock()
	h := s.histories[key.ChatID]
	kind := model.KindUser
	for _, c := range s.chats {
		if c.ID == key.ChatID {
			kind = c.Kind
		}
	}
	at := -1
	for i, m := range h.Messages {
		if m.Key.MessageID == key.MessageID {
			at = i
		}
	}
	if at < 0 || !model.Editable(h.Messages[at], kind, time.Now(), s.EditTimeLimit()) {
		s.mu.Unlock()
		return model.ErrEditForbidden
	}
	m := h.Messages[at]
	if err := model.CheckEdit(m, text); err != nil {
		s.mu.Unlock()
		return err
	}
	entities = model.SortEntities(entities)
	if model.SameText(m, text, entities) {
		s.mu.Unlock()
		return model.ErrMessageNotModified
	}
	m.Text, m.Entities, m.EditedAt = text, entities, time.Now()
	m.ContentRevision++
	h.Messages = append([]model.Message(nil), h.Messages...)
	h.Messages[at] = m
	h.Revision++
	s.histories[key.ChatID] = h
	if at == len(h.Messages)-1 {
		for i := range s.chats {
			if s.chats[i].ID == key.ChatID {
				s.chats[i].Last.Text = text
			}
		}
	}
	s.mu.Unlock()
	s.tellChanged()
	return nil
}

// EditTimeLimit is none: the demo's history is a month old, older than
// Telegram's limit, and its messages are there to be tried.
func (s *Store) EditTimeLimit() time.Duration { return 0 }
