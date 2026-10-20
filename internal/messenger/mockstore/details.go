// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"strings"

	"komarugram/internal/messenger/model"
)

// usernames are the public names of the demo's public chats.
// Новости Go has put its collectible username before its own, which the
// profile tells after it.
var usernames = map[int64]string{2: "anna_smirnova", 4: "gonews", 8: "material_design"}

// Username implements model.UsernameSource.
func (s *Store) Username(chat int64) string { return usernames[chat] }

// ChatDetails implements model.ChatDetailer: the demo's photos are spread
// over Telegram's data centers.
func (s *Store) ChatDetails(chat int64) model.ChatDetails {
	if chat <= 0 || chat == 1 {
		return model.ChatDetails{}
	}
	return model.ChatDetails{PhotoDC: int(chat%5) + 1}
}

// Online implements model.ChatDetailer: a third of a group's members.
func (s *Store) Online(_ context.Context, chat int64) (int, error) {
	for _, c := range s.chats {
		if c.ID == chat && c.Kind == model.KindGroup {
			return c.Members / 3, nil
		}
	}
	return 0, nil
}

// demoSenders is where the ids of the chats with group members the demo
// has no chat with start, apart from its other chats.
const demoSenders = 7000000000

// SenderChat implements model.SenderChatSource: the demo's chat with the
// sender when it has one by their name, or a user's by their first name,
// as Анна's messages in its groups lead to Анна Смирнова; or a new one
// with them. The comments' first message is its channel's.
func (s *Store) SenderChat(_ context.Context, m model.Message) (model.Chat, error) {
	if m.SenderID <= 0 || m.SenderName == "" {
		return model.Chat{}, model.ErrLinkNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.chats {
		if c.Title == m.SenderName || c.Kind == model.KindUser && strings.HasPrefix(c.Title, m.SenderName+" ") {
			return c, nil
		}
	}
	return model.Chat{ID: demoSenders + m.SenderID, Kind: model.KindUser, Title: m.SenderName}, nil
}
