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
	for _, m := range h.Messages {
		if !remove[m.Key.MessageID] {
			messages = append(messages, m)
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
