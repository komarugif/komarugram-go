package mockstore

import (
	"context"
	"komarugram/internal/messenger/model"
)

func (s *Store) DeleteMessages(ctx context.Context, chat int64, ids []model.MessageID, _ bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.History(chat)
	s.mu.Lock()
	defer s.mu.Unlock()
	remove := map[model.MessageID]bool{}
	for _, id := range ids {
		remove[id] = true
	}
	h := s.histories[chat]
	messages := make([]model.Message, 0, len(h.Messages))
	if s.erased == nil {
		s.erased = map[model.MessageKey]bool{}
	}
	for _, m := range h.Messages {
		if !remove[m.Key.MessageID] {
			messages = append(messages, m)
		} else {
			s.erased[m.Key] = true
		}
	}
	h.Messages = messages
	h.Revision++
	s.histories[chat] = h
	for i := range s.chats {
		if s.chats[i].ID == chat {
			s.chats[i].Last.Text = ""
			if len(messages) > 0 {
				last := messages[len(messages)-1]
				s.chats[i].Last.Text = last.Text
				s.chats[i].LastTime = last.Date
			}
		}
	}
	return nil
}

// MessageErased implements model.ErasedMessages.
func (s *Store) MessageErased(key model.MessageKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.erased[key]
}
