// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
)

// archiveLimit is the messages of a page saved to the cache: getHistory's
// most, as a scroll that goes on asks for.
const archiveLimit = pageActive

// CachedMessages implements model.ChatArchiver.
func (s *Store) CachedMessages(ctx context.Context, chat int64) (int, error) {
	c := s.history
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache == nil {
		return 0, nil
	}
	return cache.Count(ctx, chat)
}

// MessageTotal implements model.ChatArchiver: how many messages Telegram
// has, which a search for "-" counts exactly, whatever the filter leaves
// out of its page.
func (s *Store) MessageTotal(ctx context.Context, chat int64) (int, error) {
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return 0, err
	}
	defer done()
	res, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: peer.input(), Q: "-", Filter: &tg.InputMessagesFilterEmpty{}, Limit: 1})
	if err != nil {
		return 0, err
	}
	return searchTotal(res), nil
}

// ArchiveChat implements model.ChatArchiver. It goes from the newest
// message to the oldest, a page at a time, as a scroll up the history
// does, at the pace of the history's pages (historyPacer), and puts the
// messages in the cache, with the spans they make: the history reads them
// offline as it scrolls. The room it reserves for the rows not loaded
// does not grow by them: the runs saved are kept apart (archiveRanges),
// and the measurer, which lays out the rows of a span whose heights are not
// known and has the reserve grow by them, does not go into them
// (CachedFrom); the heights of those the user scrolls over are kept and
// reserved as ever. The history on screen changes only by what Telegram no
// longer has, as with any page. The runs saved are, with the spans, jumped
// over in a next saving.
func (s *Store) ArchiveChat(ctx context.Context, chat int64, report func(model.ArchiveProgress)) (err error) {
	defer crash.Recover("archive chat", func(p *crash.Panic) { err = p })
	c := s.history
	c.mu.Lock()
	api, peer, cache, parent, closing := c.api, c.peers[chat], c.cache, c.ctx, c.closing
	if api == nil || cache == nil || peer.Kind == "" || closing {
		c.mu.Unlock()
		return errNotConnected
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(parent, cancel)()

	total := -1
	if n, e := s.MessageTotal(ctx, chat); e == nil {
		total = n
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	cached, err := cache.Count(ctx, chat)
	if err != nil {
		return err
	}
	report(model.ArchiveProgress{Cached: cached, Total: total})
	historylog.Printf("archive chat=%d: %d cached of %d", chat, cached, total)

	ranges, err := loadArchived(ctx, cache, chat)
	if err != nil {
		return err
	}
	anchor := 0
	for {
		if anchor != 0 {
			// What the cache holds whole, or an earlier saving did, is not
			// asked for again.
			low, skip, e := s.archived(ctx, cache, chat, ranges, anchor-1)
			if e != nil {
				return e
			}
			if skip {
				if low <= 1 {
					return nil
				}
				anchor = low
				continue
			}
		}
		c.mu.Lock()
		start := c.generation
		c.mu.Unlock()
		// The newest page is asked for as a chat opens, the others as a
		// scroll up.
		dir := -1
		if anchor == 0 {
			dir = 0
		}
		raw, e := s.archivePage(ctx, api, peer, dir, anchor, report, cached, total)
		if e != nil {
			return e
		}
		ids := make([]int, 0, len(raw))
		for _, m := range raw {
			ids = append(ids, m.GetID())
		}
		low, high, _, ok := pageSpan(ids, dir, anchor, archiveLimit)
		held := 0
		if len(ids) > 0 {
			if held, e = cache.Have(ctx, chat, slices.Min(ids), slices.Max(ids)); e != nil {
				return e
			}
		}
		if e = s.archiveSave(ctx, chat, raw, start, dir, anchor); e != nil {
			return e
		}
		if ok {
			// Of the run, what no span held before is the saving's.
			spans, e := cache.SpansIn(ctx, chat, low, high)
			if e != nil {
				return e
			}
			for _, run := range subtractSpans(low, high, spans) {
				ranges = ranges.add(run[0], run[1])
			}
			if e = cache.Put(ctx, archivedKey(chat), ranges); e != nil {
				return e
			}
			if _, e = cache.AddSpan(ctx, chat, low, high); e != nil {
				return e
			}
		}
		if len(ids) > 0 {
			n, e := cache.Have(ctx, chat, slices.Min(ids), slices.Max(ids))
			if e != nil {
				return e
			}
			cached += max(n-held, 0)
		}
		report(model.ArchiveProgress{Cached: cached, Total: total})
		if len(ids) == 0 || low <= 1 {
			historylog.Printf("archive chat=%d: reached the start, %d cached", chat, cached)
			return nil
		}
		anchor = slices.Min(ids)
	}
}

// archivePage asks for the page of messages before anchor (the newest for
// 0), at the pace of the history, waiting as long as Telegram tells to.
func (s *Store) archivePage(ctx context.Context, api *tg.Client, peer peerRecord, dir, anchor int, report func(model.ArchiveProgress), cached, total int) ([]tg.MessageClass, error) {
	c := s.history
	req := historyRequest(peer.input(), dir, anchor, archiveLimit)
	for {
		if _, err := c.pace.wait(ctx); err != nil {
			return nil, err
		}
		res, err := api.MessagesGetHistory(ctx, req)
		if err == nil {
			modified, ok := res.AsModified()
			if !ok {
				return nil, errors.New("messages.getHistory: not modified")
			}
			s.rememberPeers(modified.GetUsers(), modified.GetChats())
			return modified.GetMessages(), nil
		}
		wait, flood := tgerr.AsFloodWait(err)
		if !flood {
			return nil, err
		}
		historylog.Printf("archive chat=%d: asked to wait %s", peer.ID, wait)
		report(model.ArchiveProgress{Cached: cached, Total: total, Wait: wait})
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
		report(model.ArchiveProgress{Cached: cached, Total: total})
	}
}

// archiveSave puts a page into the cache, and takes out of it what Telegram
// no longer has in the page's range.
func (s *Store) archiveSave(ctx context.Context, chat int64, raw []tg.MessageClass, start uint64, dir, anchor int) error {
	if err := s.reconcile(ctx, chat, raw, start, dir, anchor, archiveLimit); err != nil {
		return err
	}
	// The media's locations are kept in the cache, which finds them when
	// they are asked for, and not in memory: a chat of a million messages
	// would fill it.
	_, err := s.ingest(context.WithValue(ctx, archivingKey{}, true), raw, false, start)
	return err
}

// archivingKey marks the context of a page saved by ArchiveChat.
type archivingKey struct{}

// subtractSpans is low..high less the spans, in runs.
func subtractSpans(low, high int, spans []historycache.Span) [][2]int {
	var out [][2]int
	for _, sp := range spans {
		if sp.Low > low {
			out = append(out, [2]int{low, min(sp.Low-1, high)})
		}
		low = max(low, sp.High+1)
		if low > high {
			return out
		}
	}
	return append(out, [2]int{low, high})
}

// archiveRanges are the runs of ids of a chat that saving its messages put
// in the cache, whole, by their low and high ends, in order, apart.
type archiveRanges [][2]int

func archivedKey(chat int64) string { return fmt.Sprintf("archive/%d", chat) }

func loadArchived(ctx context.Context, cache *historycache.Cache, chat int64) (archiveRanges, error) {
	var r archiveRanges
	_, err := cache.Get(ctx, archivedKey(chat), &r)
	return r, err
}

// add joins low..high to the runs, with those it overlaps or touches.
func (r archiveRanges) add(low, high int) archiveRanges {
	out := make(archiveRanges, 0, len(r)+1)
	for _, run := range r {
		if run[1]+1 >= low && run[0] <= high+1 {
			low, high = min(low, run[0]), max(high, run[1])
			continue
		}
		out = append(out, run)
	}
	out = append(out, [2]int{low, high})
	slices.SortFunc(out, func(a, b [2]int) int { return a[0] - b[0] })
	return out
}

// has returns the run that holds id.
func (r archiveRanges) has(id int) (low int, ok bool) {
	for _, run := range r {
		if run[0] <= id && id <= run[1] {
			return run[0], true
		}
	}
	return 0, false
}

// archived tells whether id is in a run the cache keeps whole or a saving
// put in it, and where that run begins.
func (s *Store) archived(ctx context.Context, cache *historycache.Cache, chat int64, ranges archiveRanges, id int) (low int, ok bool, err error) {
	span, found, legacy, err := cache.SpanOf(ctx, chat, id)
	if err != nil {
		return 0, false, err
	}
	low, ok = ranges.has(id)
	if found && !legacy && (!ok || span.Low < low) {
		low, ok = span.Low, true
	}
	return low, ok, nil
}
