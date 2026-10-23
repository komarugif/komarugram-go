// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// demoReadOutbox is how far Анна read the account's messages in the demo:
// those after it have one check.
const demoReadOutbox = 300

// The times a message sent in the demo takes to reach Telegram, and to be
// read by a user.
const (
	demoDelivery = 1200 * time.Millisecond
	demoReading  = 4 * time.Second
)

// demoPresence is when the demo's users were online, from now: Анна is
// online, the others were at times that show each way of telling it.
func demoPresence(now time.Time) map[int64]model.Presence {
	return map[int64]model.Presence{
		2:  {Seen: model.SeenAt, At: now.Add(365 * 24 * time.Hour)},
		5:  {Seen: model.SeenAt, At: now.Add(-25 * time.Minute)},
		9:  {Seen: model.SeenAt, At: now.Add(-30 * time.Hour)},
		12: {Seen: model.SeenRecently},
		15: {Seen: model.SeenLongAgo},
	}
}

// Presence implements model.PresenceSource.
func (s *Store) Presence(chat int64) (model.Presence, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.presence[chat]
	return p, ok
}

// RefreshPresence implements model.PresenceSource: the demo knows all
// already.
func (s *Store) RefreshPresence(int64) {}

// deliver takes message id of chat, sent on its way, after demoDelivery,
// and has it read after demoReading in a private chat.
func (s *Store) deliver(chat int64, id model.MessageID) {
	defer crash.Recover("demo delivery", nil)
	time.Sleep(demoDelivery)
	s.mu.Lock()
	h := s.histories[chat]
	h.Messages = append([]model.Message(nil), h.Messages...)
	for i := range h.Messages {
		if m := &h.Messages[i]; m.Key.MessageID == id && m.Sending {
			m.Sending = false
			m.ContentRevision++
		}
	}
	h.Revision++
	s.histories[chat] = h
	private := false
	for i := range s.chats {
		if s.chats[i].ID == chat {
			private = s.chats[i].Kind == model.KindUser
			s.chats[i].Last.Sent, s.chats[i].Last.ID = true, id
		}
	}
	s.mu.Unlock()
	s.tellChanged()
	if !private {
		return
	}
	time.Sleep(demoReading)
	s.mu.Lock()
	for i := range s.chats {
		if s.chats[i].ID == chat {
			s.chats[i].ReadOutbox = max(s.chats[i].ReadOutbox, id)
		}
	}
	s.mu.Unlock()
	s.tellChanged()
}
