// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
)

// A page the cache has whole but cannot vouch for (history_verified.go), as
// a channel's after a restart, is shown at once and its range checked
// against Telegram's when the scroll rests: asked with the hash of what the
// cache has, a range that did not change comes back as
// messages.messagesNotModified, and is verified; one that changed comes
// whole, and replaces the cache's and the history's in place. Until then
// the page may show a message deleted meanwhile, or a text before its edit.
//
// The checks keep out of the scroll's way: one at a time, only when no page
// was asked or shown for checkRest, and only while the pacer's window has
// room for checkReserve requests of a scroll that resumes. A range is
// dropped when its chain ended, its chat is not shown, or it is more than
// checkNear messages from where the chat is viewed.
const (
	checkRest    = 1500 * time.Millisecond
	checkReserve = 10
	checkQueue   = 8
	checkNear    = 160
	// checkLimit is as many messages as a range checked may hold, a page
	// of a scroll at most: min_id bounds what comes.
	checkLimit = pageActive
)

// rangeCheck is a range of a chat's cache to check, and the chain it was
// shown in.
type rangeCheck struct {
	chat      int64
	low, high int
	chain     int64
	chained   bool
}

// historyChecks is the queue of the ranges to check, newest first.
type historyChecks struct {
	mu      sync.Mutex
	queue   []rangeCheck
	running bool
	// active is when a page was last asked or shown; paused, until when a
	// FLOOD_WAIT stops the checks.
	active, paused time.Time
	timer          *time.Timer
	// rest is checkRest, or what a test sets.
	rest time.Duration
}

// restFor is how long the scroll rests before a check. The caller holds
// k.mu.
func (k *historyChecks) restFor() time.Duration {
	if k.rest != 0 {
		return k.rest
	}
	return checkRest
}

// runAfter has runChecks called after d. The caller holds k.mu.
func (s *Store) runChecksAfter(d time.Duration) {
	k := &s.history.checks
	if k.timer == nil {
		k.timer = time.AfterFunc(d, s.runChecks)
		return
	}
	k.timer.Reset(d)
}

// pageActivity tells the checks a page was asked or shown: the scroll goes
// on, and they wait for it to rest. It tells whether the page before was
// asked within pageActiveGap: a scroll that goes on, whose pages are
// pageActive messages.
func (s *Store) pageActivity() bool {
	k := &s.history.checks
	k.mu.Lock()
	defer k.mu.Unlock()
	now := time.Now()
	prev := k.active
	k.active = now
	if len(k.queue) > 0 {
		s.runChecksAfter(k.restFor())
	}
	return !prev.IsZero() && now.Sub(prev) < pageActiveGap
}

// queueCheck queues r, first; one queued of the same range of the chat is
// replaced.
func (s *Store) queueCheck(r rangeCheck) {
	k := &s.history.checks
	k.mu.Lock()
	defer k.mu.Unlock()
	k.queue = slices.DeleteFunc(k.queue, func(q rangeCheck) bool {
		return q.chat == r.chat && q.low <= r.high && r.low <= q.high
	})
	k.queue = append([]rangeCheck{r}, k.queue...)
	if len(k.queue) > checkQueue {
		k.queue = k.queue[:checkQueue]
	}
	historylog.Printf("check queued chat=%d %d..%d (%d queued)", r.chat, r.low, r.high, len(k.queue))
	s.runChecksAfter(k.restFor())
}

// runChecks starts the next check, when the scroll rests and the window
// has room, or has itself called again when it may.
func (s *Store) runChecks() {
	defer crash.Recover("history checks", nil)
	c := s.history
	k := &c.checks
	k.mu.Lock()
	if k.running || len(k.queue) == 0 {
		k.mu.Unlock()
		return
	}
	now := time.Now()
	if wait := k.restFor() - now.Sub(k.active); wait > 0 {
		s.runChecksAfter(wait)
		k.mu.Unlock()
		return
	}
	if now.Before(k.paused) {
		s.runChecksAfter(k.paused.Sub(now))
		k.mu.Unlock()
		return
	}
	if c.pace.used() >= paceBudget-checkReserve {
		s.runChecksAfter(time.Second)
		k.mu.Unlock()
		return
	}
	r := k.queue[0]
	k.queue = k.queue[1:]
	k.running = true
	k.mu.Unlock()

	done := func() {
		k.mu.Lock()
		k.running = false
		if len(k.queue) > 0 {
			s.runChecksAfter(0)
		}
		k.mu.Unlock()
	}
	c.mu.Lock()
	closing := c.closing
	c.mu.Unlock()
	if closing {
		done()
		return
	}
	c.wg.Go(func() {
		defer done()
		defer crash.Recover("history check", nil)
		s.check(r)
	})
}

// checkDrop is why r is not to be checked now, or "".
func (s *Store) checkDrop(r rangeCheck) string {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api == nil {
		return "offline"
	}
	if chain, ok := c.chainOf(r.chat); ok != r.chained || ok && chain != r.chain {
		return "its chain ended"
	}
	if !c.watch.shown(r.chat) {
		return "its chat is not shown"
	}
	h := c.histories[r.chat]
	if h == nil || len(h.Messages) == 0 {
		return "its history is gone"
	}
	ms := h.Messages
	at := func(id int) int {
		return sort.Search(len(ms), func(i int) bool { return int(ms[i].Key.MessageID) >= id })
	}
	view := len(ms) - 1
	if v, ok := c.views[r.chat]; ok && !v.AtEnd {
		view = at(int(v.AnchorMessageID))
	}
	low, high := at(r.low), at(r.high+1)-1
	if low-view > checkNear || view-high > checkNear {
		return "it is far from the view"
	}
	return ""
}

// check checks r against Telegram's messages of its range.
func (s *Store) check(r rangeCheck) {
	if why := s.checkDrop(r); why != "" {
		historylog.Printf("check chat=%d %d..%d dropped: %s", r.chat, r.low, r.high, why)
		return
	}
	c := s.history
	c.mu.Lock()
	api, peer, start, cache, parent := c.api, c.peers[r.chat], c.generation, c.cache, c.ctx
	c.mu.Unlock()
	if api == nil || cache == nil || peer.Kind == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if r.chained {
		if ok, err := cache.Verified(ctx, r.chat, r.low, r.high, r.chain); err != nil || ok {
			return
		}
	}
	cached, err := cache.Range(ctx, r.chat, r.low, r.high, checkLimit)
	if err != nil {
		historylog.Printf("check chat=%d %d..%d: cache: %v", r.chat, r.low, r.high, err)
		return
	}
	hash := historyHash(cached)
	if _, err = c.pace.wait(ctx); err != nil {
		return
	}
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer.input(), OffsetID: r.high + 1, MinID: r.low - 1, Limit: checkLimit, Hash: hash})
	if err != nil {
		if wait, ok := tgerr.AsFloodWait(err); ok {
			k := &c.checks
			k.mu.Lock()
			k.paused = time.Now().Add(wait)
			k.mu.Unlock()
		}
		historylog.Printf("check chat=%d %d..%d failed: %v", r.chat, r.low, r.high, err)
		return
	}
	modified, ok := res.AsModified()
	if !ok {
		historylog.Printf("check chat=%d %d..%d: not modified", r.chat, r.low, r.high)
		if r.chained {
			_ = s.verify(ctx, cache, r.chat, r.low, r.high, r.chain)
		}
		return
	}
	raw := modified.GetMessages()
	historylog.Printf("check chat=%d %d..%d: modified, %d messages for %d kept", r.chat, r.low, r.high, len(raw), len(cached))
	s.rememberPeers(modified.GetUsers(), modified.GetChats())
	if err = s.reconcileRange(ctx, r.chat, raw, start, r.low, r.high); err != nil {
		return
	}
	msgs, err := s.ingest(ctx, raw, false, start)
	if err != nil {
		return
	}
	if err = s.persistDialogs(ctx); err != nil {
		return
	}
	if r.chained {
		_ = s.verify(ctx, cache, r.chat, r.low, r.high, r.chain)
	}
	s.mergeChecked(r.chat, msgs, start)
}

// mergeChecked puts msgs, a range checked, into the chat's history in
// place: an edit replaces the message, one the history lacks within what
// it holds is added. What reconcileRange took away is gone already; what
// changed by an update newer than start stays.
func (s *Store) mergeChecked(chat int64, msgs []model.Message, start uint64) {
	c := s.history
	c.mu.Lock()
	h := c.histories[chat]
	if h == nil || len(h.Messages) == 0 {
		c.mu.Unlock()
		return
	}
	first, last := h.Messages[0].Key.MessageID, h.Messages[len(h.Messages)-1].Key.MessageID
	at := map[model.MessageID]int{}
	for i, m := range h.Messages {
		at[m.Key.MessageID] = i
	}
	added := false
	for _, m := range msgs {
		if c.touched[m.Key] > start || c.deleted[m.Key] && !m.Deleted {
			continue
		}
		if i, ok := at[m.Key.MessageID]; ok {
			h.Messages[i] = m
		} else if m.Key.MessageID > first && m.Key.MessageID < last {
			h.Messages = append(h.Messages, m)
			added = true
		}
	}
	if added {
		sort.Slice(h.Messages, func(i, j int) bool { return h.Messages[i].Key.MessageID < h.Messages[j].Key.MessageID })
	}
	h.Revision++
	c.mu.Unlock()
	s.changed()
}

// historyHash is the hash messages.getHistory takes for msgs, a range of
// the cache: Telegram answers messages.messagesNotModified when its messages
// of the range fold to it. It folds, newest first, each message's id and
// then its edit date, shown or hidden (edit_hide), or its date when it was
// not edited (checked live, in Saved Messages and in groups). A message
// kept before its hidden edit date was makes the range come whole once.
// Messages Telegram no longer has (kept deleted) and local ones are left
// out.
func historyHash(msgs []model.Message) int64 {
	ms := slices.Clone(msgs)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Key.MessageID > ms[j].Key.MessageID })
	var h uint64
	add := func(v int64) {
		h ^= h >> 21
		h ^= h << 35
		h ^= h >> 4
		h += uint64(v)
	}
	for _, m := range ms {
		if m.Deleted || m.Local() {
			continue
		}
		add(int64(m.Key.MessageID))
		if !m.EditedAt.IsZero() {
			add(m.EditedAt.Unix())
		} else if !m.HiddenEditAt.IsZero() {
			add(m.HiddenEditAt.Unix())
		} else {
			add(m.Date.Unix())
		}
	}
	return int64(h)
}

// stopChecks stops the checks, when the store closes.
func (s *Store) stopChecks() {
	k := &s.history.checks
	k.mu.Lock()
	defer k.mu.Unlock()
	k.queue = nil
	if k.timer != nil {
		k.timer.Stop()
	}
}
