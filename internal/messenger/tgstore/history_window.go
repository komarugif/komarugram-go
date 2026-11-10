// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"

	"komarugram/internal/crash"
	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
)

// A chat's loaded history is a window of it around where it is viewed: as
// a page comes at one end, the messages farthest from the view, at the
// other, are let go once there are more than historyWindow, and come
// again from the cache when the view goes back to them. The history marks
// that end as having more, older or newer, so that its pages are asked
// for; the UI keeps the rows' room meanwhile.

const (
	// historyWindow is the most messages a chat's loaded history keeps.
	historyWindow = 600
	// windowMargin is how many messages, at the least, the window keeps
	// past the message viewed on the side it lets go of.
	windowMargin = 150
)

// trimWindow lets go of the messages of h, chat's history, that a page in
// dir took past historyWindow: at the end the page did not come to, not
// within windowMargin of the message viewed, nor between the parts of an
// album. The caller holds c.mu.
func (c *conversation) trimWindow(chat int64, h *model.History, dir int) {
	n := len(h.Messages)
	if dir == 0 || n <= historyWindow {
		return
	}
	viewed := -1
	if v, ok := c.views[chat]; ok {
		if v.AtEnd {
			viewed = n - 1
		}
		for i, m := range h.Messages {
			if m.Key.MessageID == v.AnchorMessageID {
				viewed = i
				break
			}
		}
	}
	if dir > 0 {
		drop := n - historyWindow
		if viewed >= 0 {
			drop = min(drop, viewed-windowMargin)
		}
		for drop > 0 && drop < n && sameAlbum(h.Messages[drop-1], h.Messages[drop]) {
			drop--
		}
		if drop <= 0 {
			return
		}
		historylog.Printf("window chat=%d: %d older messages let go, %d..%d kept", chat, drop, h.Messages[drop].Key.MessageID, h.Messages[n-1].Key.MessageID)
		// Copied, so that the array of all of them goes.
		h.Messages = append([]model.Message(nil), h.Messages[drop:]...)
		h.HasOlder = true
		return
	}
	keep := historyWindow
	if viewed >= 0 {
		keep = max(keep, viewed+windowMargin+1)
	}
	for keep > 0 && keep < n && sameAlbum(h.Messages[keep-1], h.Messages[keep]) {
		keep++
	}
	if keep >= n {
		return
	}
	historylog.Printf("window chat=%d: %d newer messages let go, %d..%d kept", chat, n-keep, h.Messages[0].Key.MessageID, h.Messages[keep-1].Key.MessageID)
	h.Messages = append([]model.Message(nil), h.Messages[:keep]...)
	h.HasNewer = true
}

// sameAlbum tells whether a and b are parts of one album, as GroupAlbums
// makes them one row.
func sameAlbum(a, b model.Message) bool {
	return a.GroupedID != 0 && a.GroupedID == b.GroupedID && a.Media != nil && b.Media != nil && a.Key.ChatID == b.Key.ChatID
}

// leapWindow is how many messages a leap of the window brings: as many on
// each side of the message it leaps to.
const leapWindow = historyWindow / 2

// LoadAround moves chat's loaded history to the messages the cache keeps
// around message id, at once, rather than page by page to it
// (model.HistoryWindow): the view went far into the rows the UI keeps the
// room of. It tells whether it started; it does not while a page loads.
func (s *Store) LoadAround(chat int64, id model.MessageID) bool {
	if isThread(chat) {
		return false
	}
	c := s.history
	c.mu.Lock()
	h := c.histories[chat]
	if c.closing || c.cache == nil || h == nil || h.LoadingOlder || h.LoadingNewer || len(h.Messages) == 0 || id <= 0 {
		c.mu.Unlock()
		return false
	}
	h.LoadingOlder, h.LoadingNewer = true, true
	epoch, cache := c.epochs[chat], c.cache
	c.mu.Unlock()
	c.wg.Go(func() {
		var msgs []model.Message
		var err error
		defer func() {
			// Whatever came, the history loads again: a panic too ends it.
			s.leapt(chat, epoch, id, msgs, err)
		}()
		defer crash.Recover("history leap", func(p *crash.Panic) { err = p })
		start := diagnostics.Start()
		msgs, err = cache.Around(c.ctx, chat, int(id), 2*leapWindow)
		s.profileHistory("cache.around-read", chat, 0, int(id), len(msgs), start, err)
		for i, m := range msgs {
			msgs[i] = s.applyLocalEmoji(context.Background(), m)
		}
	})
	return true
}

// leapt puts msgs, the window read around message id, in the place of
// chat's loaded history, unless it started again since (epoch).
func (s *Store) leapt(chat int64, epoch uint64, id model.MessageID, msgs []model.Message, err error) {
	c := s.history
	c.mu.Lock()
	h := c.histories[chat]
	if h == nil || c.epochs[chat] != epoch {
		c.mu.Unlock()
		return
	}
	h.LoadingOlder, h.LoadingNewer = false, false
	kept := msgs[:0]
	for _, m := range msgs {
		gone := c.deleted[m.Key] || m.Key.ChatID > -1000000000000 && c.globalDeleted[int(m.Key.MessageID)]
		if !gone || m.Deleted {
			kept = append(kept, m)
		}
	}
	if err == nil && len(kept) > 0 {
		h.Messages = kept
		first, last := int(kept[0].Key.MessageID), int(kept[len(kept)-1].Key.MessageID)
		h.HasOlder = c.firsts[chat] == 0 || c.firsts[chat] != first
		h.HasNewer = c.top[chat] == 0 || last < c.top[chat]
		historylog.Printf("leap chat=%d to %d: %d messages, %d..%d", chat, id, len(kept), first, last)
	} else {
		historylog.Printf("leap chat=%d to %d: nothing, err=%v", chat, id, err)
	}
	h.Revision++
	c.mu.Unlock()
	s.changed()
}
