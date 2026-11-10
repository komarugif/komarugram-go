// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"

	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/model"
)

// Reserve tells what the cache keeps of chat's history after message after
// (model.HistoryReserve).
func (s *Store) Reserve(chat int64, after model.MessageID, env model.RenderEnvironment, version byte, limit int) []model.ReserveRow {
	if isThread(chat) {
		return nil
	}
	c := s.history
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache == nil {
		return nil
	}
	start := diagnostics.Start()
	rows, e := cache.Reserve(c.ctx, chat, int(after), env, version, limit)
	s.profileHistory("cache.reserve-read", chat, 0, int(after), len(rows), start, e)
	if e != nil {
		s.historyError(chat, e)
	}
	return rows
}

// Profiled tells the revisions of the messages of chat whose rows'
// profiles the cache keeps, laid out in env at any width
// (model.RowProfiles). They come to it with the layouts (SaveView).
func (s *Store) Profiled(chat int64, env model.RenderEnvironment, version byte) map[model.MessageID]uint64 {
	if isThread(chat) {
		return nil
	}
	c := s.history
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache == nil {
		return nil
	}
	start := diagnostics.Start()
	profiled, e := cache.Profiled(c.ctx, chat, env, version)
	s.profileHistory("cache.profiled-read", chat, 0, 0, len(profiled), start, e)
	if e != nil {
		s.historyError(chat, e)
	}
	return profiled
}

// CachedFrom reads the messages of chat the cache keeps from message from
// on, as far as it keeps the history whole with it (model.CachedHistory):
// as a page from the cache brings them, without those deleted, and up to
// the first of the run ArchiveChat saved after the first of them.
func (s *Store) CachedFrom(chat int64, from model.MessageID, limit int) []model.Message {
	if isThread(chat) {
		return nil
	}
	c := s.history
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache == nil {
		return nil
	}
	start := diagnostics.Start()
	msgs, e := cache.From(c.ctx, chat, int(from), limit)
	s.profileHistory("cache.from-read", chat, 1, int(from), len(msgs), start, e)
	if e != nil {
		s.historyError(chat, e)
		return nil
	}
	c.mu.Lock()
	kept := msgs[:0]
	for _, m := range msgs {
		// A message kept deleted stays, as the history keeps its tombstone.
		gone := c.deleted[m.Key] || m.Key.ChatID > -1000000000000 && c.globalDeleted[int(m.Key.MessageID)]
		if gone && !m.Deleted {
			continue
		}
		kept = append(kept, m)
	}
	c.mu.Unlock()
	// The run ArchiveChat saved is not measured: the rows of it the user
	// scrolls over are kept with their heights and reserved as ever, but
	// the measurer does not lay out the rest to grow the reserve by it.
	if ranges, err := loadArchived(c.ctx, cache, chat); err == nil && len(ranges) > 0 {
		for i := 1; i < len(kept); i++ {
			if _, saved := ranges.has(int(kept[i].Key.MessageID)); saved {
				kept = kept[:i]
				break
			}
		}
	}
	for i, m := range kept {
		kept[i] = s.applyLocalEmoji(context.Background(), m)
	}
	return kept
}

// CachedLookup is message id of chat as the store has it without Telegram
// (model.CachedHistory): looked up before, or in the cache, in the chat's
// history or kept as looked up; as LookupMessage finds it.
func (s *Store) CachedLookup(chat int64, id model.MessageID) (model.Message, model.LookupState, bool) {
	c := s.history
	key := model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: id}
	c.mu.Lock()
	cache := c.cache
	if l := c.lookups[key]; l != nil && l.state != model.LookupLoading {
		c.mu.Unlock()
		return l.msg, l.state, true
	}
	c.mu.Unlock()
	if cache == nil || id <= 0 {
		return model.Message{}, model.LookupLoading, false
	}
	if m, ok, err := cache.Message(c.ctx, chat, int(id)); err == nil && ok {
		return m, model.LookupFound, true
	}
	var m model.Message
	if ok, err := cache.Get(c.ctx, fmt.Sprintf("lookup/%d/%d", chat, id), &m); err == nil && ok {
		return m, model.LookupFound, true
	}
	return model.Message{}, model.LookupLoading, false
}

// ProfilesBetween reads the profiles the cache keeps of the rows of chat's
// messages low to high, laid out in env at any width
// (model.RowShapes).
func (s *Store) ProfilesBetween(chat int64, env model.RenderEnvironment, version byte, low, high model.MessageID) map[model.MessageID][]byte {
	if isThread(chat) {
		return nil
	}
	c := s.history
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache == nil {
		return nil
	}
	start := diagnostics.Start()
	out, e := cache.ProfilesBetween(c.ctx, chat, env, version, low, high)
	s.profileHistory("cache.profiles-read", chat, 0, int(low), len(out), start, e)
	if e != nil {
		s.historyError(chat, e)
	}
	return out
}
