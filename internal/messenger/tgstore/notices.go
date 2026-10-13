// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// noticeAge is how old a message may be and still be told of: older ones
// come with the catch-up after a reconnection, and are not news.
const noticeAge = 10 * time.Minute

// SetNotices sets the function called, on the store's goroutine, with each
// new incoming message; nil stops it.
func (s *Store) SetNotices(f func(model.MessageNotice)) {
	if f == nil {
		s.notices.Store(nil)
		return
	}
	s.notices.Store(&f)
}

// notice tells of m, a new message of chat that came in raw.
func (s *Store) notice(m model.Message, chat model.Chat, raw tg.MessageClass) {
	f := s.notices.Load()
	if f == nil || chat.ID == 0 || m.Outgoing || m.Kind == model.MessageService || chat.Kind == model.KindSaved || time.Since(m.Date) > noticeAge {
		return
	}
	n := model.MessageNotice{Chat: chat, Message: messagePreview(m, chat.Kind == model.KindGroup)}
	if r, ok := raw.(*tg.Message); ok {
		n.Silent = r.Silent
	}
	(*f)(n)
}

// mutedNow tells whether settings mute their chat now.
func mutedNow(settings tg.PeerNotifySettings) bool {
	until, ok := settings.GetMuteUntil()
	return ok && time.Unix(int64(until), 0).After(time.Now())
}

// setMuted applies a change of a chat's notification settings.
func (s *Store) setMuted(id int64, settings tg.PeerNotifySettings) {
	muted := mutedNow(settings)
	s.mu.Lock()
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		if chats[i].ID == id {
			chats[i].Muted = muted
		}
	}
	s.chats = chats
	s.mu.Unlock()
}
