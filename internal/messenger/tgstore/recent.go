// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// recentKey is where the search history is kept in the account's cache,
// which is encrypted with it.
const recentKey = "recent_chats"

// recentChats is the search history.
type recentChats struct {
	mu   sync.Mutex
	list []model.Chat
	// saving lets one save through at a time; each writes the list as it
	// is then, so the last one written is the last change.
	saving sync.Mutex
}

// RecentChats implements model.RecentChats. A chat still in the chat list is
// named as the list names it now.
func (s *Store) RecentChats() []model.Chat {
	s.recent.mu.Lock()
	out := append([]model.Chat(nil), s.recent.list...)
	s.recent.mu.Unlock()
	live := map[int64]model.Chat{}
	for _, c := range s.Chats() {
		live[c.ID] = c
	}
	for i, c := range out {
		if now, ok := live[c.ID]; ok {
			out[i].Title, out[i].Kind, out[i].Badges, out[i].Muted = now.Title, now.Kind, now.Badges, now.Muted
		} else if badges, ok := s.PeerBadges(c.ID); ok {
			// A chat with no dialog gets no updates of its status: what the
			// profile or a history brought later is newer than what was kept.
			out[i].Badges = badges
		}
	}
	return out
}

// PeerBadges implements model.PeerBadgesSource.
func (s *Store) PeerBadges(id int64) (model.Badges, bool) {
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	p, ok := s.history.peers[id]
	if !ok || !p.Metadata.Known || p.Rights.Self {
		return model.Badges{}, false
	}
	return p.Metadata.Badges, true
}

// BumpRecentChat implements model.RecentChats.
func (s *Store) BumpRecentChat(c model.Chat) {
	s.changeRecent(func(list []model.Chat) []model.Chat { return model.BumpChat(list, c, model.RecentChatsLimit) })
}

// RemoveRecentChat implements model.RecentChats.
func (s *Store) RemoveRecentChat(id int64) {
	s.changeRecent(func(list []model.Chat) []model.Chat {
		out := list[:0:0]
		for _, c := range list {
			if c.ID != id {
				out = append(out, c)
			}
		}
		return out
	})
}

// ClearRecentChats implements model.RecentChats.
func (s *Store) ClearRecentChats() {
	s.changeRecent(func([]model.Chat) []model.Chat { return nil })
}

// changeRecent changes the history and saves it, off the frame.
func (s *Store) changeRecent(change func([]model.Chat) []model.Chat) {
	s.recent.mu.Lock()
	s.recent.list = change(s.recent.list)
	s.recent.mu.Unlock()
	s.changed()
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	cache := c.cache
	if c.closing || cache == nil {
		return
	}
	c.wg.Go(func() {
		defer crash.Recover("search history", nil)
		s.recent.saving.Lock()
		defer s.recent.saving.Unlock()
		s.recent.mu.Lock()
		list := append([]model.Chat(nil), s.recent.list...)
		s.recent.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = cache.Put(ctx, recentKey, list)
	})
}
