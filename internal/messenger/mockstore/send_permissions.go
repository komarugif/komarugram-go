package mockstore

import (
	"context"
	"errors"
	"komarugram/internal/messenger/model"
	"time"
)

// SendPermissions gives the demo representative channel and group states.
func (s *Store) SendPermissions(chat int64) model.SendPermissions {
	switch chat {
	case 4:
		return model.SendPermissions{Unavailable: true, Broadcast: true, DiscussionID: 3, DirectMessagesID: demoDirectMessages}
	case 8:
		return model.SendPermissions{Unavailable: true, Broadcast: true}
	case 7:
		return model.SendPermissions{Default: model.SendPhoto | model.SendVoice | model.SendGIF}
	case 10:
		return model.SendPermissions{Personal: model.SendText, Until: s.created.Add(24 * time.Hour).Unix()}
	default:
		return model.SendPermissions{}
	}
}
func (s *Store) Discussion(ctx context.Context, chat int64) (model.Chat, error) {
	if err := ctx.Err(); err != nil {
		return model.Chat{}, err
	}
	id := s.SendPermissions(chat).DiscussionID
	for _, c := range s.Chats() {
		if c.ID == id {
			return c, nil
		}
	}
	return model.Chat{}, errors.New("no discussion group")
}

// demoDirectMessages is the chat of Новости Go's direct messages, not in
// the list until it is opened, as a channel's are.
const demoDirectMessages = 40

// DirectMessages implements model.DirectMessagesSource.
func (s *Store) DirectMessages(ctx context.Context, chat int64) (model.Chat, error) {
	if err := ctx.Err(); err != nil {
		return model.Chat{}, err
	}
	if s.SendPermissions(chat).DirectMessagesID != demoDirectMessages {
		return model.Chat{}, errors.New("the channel takes no direct messages")
	}
	return model.Chat{ID: demoDirectMessages, Title: "Новости Go", Kind: model.KindGroup}, nil
}

// ChatMuted implements model.NotificationSetter.
func (s *Store) ChatMuted(chat int64) bool {
	for _, c := range s.Chats() {
		if c.ID == chat {
			return c.Muted
		}
	}
	return false
}

// SetChatMuted implements model.NotificationSetter: the demo's chats only.
func (s *Store) SetChatMuted(_ context.Context, chat int64, muted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		if chats[i].ID == chat {
			chats[i].Muted = muted
		}
	}
	s.chats = chats
	return nil
}
