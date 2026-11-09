// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"time"

	"komarugram/internal/messenger/model"
)

// demoArchivePage is how many messages a step of the demo's saving brings,
// and demoArchiveStep how long a step takes.
const (
	demoArchivePage = 400
	demoArchiveStep = 120 * time.Millisecond
)

// demoMessages is how many messages a demo chat has: its history is a
// sample of them.
func (s *Store) demoMessages(chat int64) int {
	return max(len(s.History(chat).Messages), 1) * 12
}

// CachedMessages implements model.ChatArchiver: the demo's history, and what
// its saving has brought.
func (s *Store) CachedMessages(_ context.Context, chat int64) (int, error) {
	return s.demoCached(chat), nil
}

// demoCached is the demo's history and what its saving has brought.
func (s *Store) demoCached(chat int64) int {
	n := len(s.History(chat).Messages)
	s.mu.Lock()
	defer s.mu.Unlock()
	return n + s.archived[chat]
}

// MessageTotal implements model.ChatArchiver.
func (s *Store) MessageTotal(_ context.Context, chat int64) (int, error) {
	return s.demoMessages(chat), nil
}

// ArchiveChat implements model.ChatArchiver: the demo's chats gain their
// messages by steps, until all are there or ctx ends; nothing reaches
// Telegram.
func (s *Store) ArchiveChat(ctx context.Context, chat int64, report func(model.ArchiveProgress)) error {
	total := s.demoMessages(chat)
	for {
		cached := s.demoCached(chat)
		report(model.ArchiveProgress{Cached: cached, Total: total})
		if cached >= total {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(demoArchiveStep):
		}
		s.mu.Lock()
		if s.archived == nil {
			s.archived = map[int64]int{}
		}
		s.archived[chat] += min(demoArchivePage, total-cached)
		s.mu.Unlock()
	}
}
