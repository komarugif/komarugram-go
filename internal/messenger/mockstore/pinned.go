// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import "komarugram/internal/messenger/model"

// demoPinned are the messages pinned in each demo chat: a few along its
// history, and the one its pin service message names.
var demoPinned = []model.MessageID{35, 140, 240, 330}

// PinnedMessages returns the demo chat's pinned messages until they are
// hidden.
func (s *Store) PinnedMessages(chat int64) []model.MessageID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pinnedHidden[chat] {
		return nil
	}
	return demoPinned
}

func (s *Store) HidePinned(chat int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pinnedHidden == nil {
		s.pinnedHidden = map[int64]bool{}
	}
	s.pinnedHidden[chat] = true
}

// LookupMessage finds a message of the demo chat's history.
func (s *Store) LookupMessage(chat int64, id model.MessageID) (model.Message, model.LookupState) {
	// A channel's first message tells its creation, as Telegram's does, over
	// the demo's own.
	if id == 1 {
		for _, c := range s.chats {
			if c.ID == chat && c.Kind == model.KindChannel {
				return model.Message{Key: model.MessageKey{ChatID: chat, MessageID: 1}, Date: s.created.AddDate(-1, -2, -3), Post: true,
					Service: &model.ServiceAction{Kind: model.ServiceCreateChannel, Title: c.Title}}, model.LookupFound
			}
		}
	}
	for _, m := range s.History(chat).Messages {
		if m.Key.MessageID == id {
			return m, model.LookupFound
		}
	}
	return model.Message{}, model.LookupGone
}
