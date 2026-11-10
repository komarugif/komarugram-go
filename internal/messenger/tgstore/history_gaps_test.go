// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// historyPage answers req as Telegram does over a chat of ids, oldest
// first: the window that starts add_offset messages from the place of
// offset_id in the newest-first list (see serveHistory).
func historyPage(ids []int, req *tg.MessagesGetHistoryRequest) []int {
	newest := slices.Clone(ids)
	slices.Reverse(newest)
	at := 0
	if req.OffsetID != 0 {
		at = len(newest)
		for i, id := range newest {
			if id < req.OffsetID {
				at = i
				break
			}
		}
	}
	// Telegram cuts the window at the newest message; it does not
	// move it: a page around a message near the end has fewer.
	from := at + req.AddOffset
	to := min(from+req.Limit, len(newest))
	from = max(from, 0)
	if from >= to {
		return nil
	}
	return newest[from:to]
}

// serveIDs is serveHistory over any ids, as in private chats, whose IDs are
// shared by the account's chats and are far from one after another.
func serveIDs(ids []int) func(context.Context, bin.Encoder, bin.Decoder) error {
	return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesGetHistoryRequest)
		if !ok {
			return nil
		}
		var msgs []tg.MessageClass
		for _, id := range historyPage(ids, req) {
			msgs = append(msgs, &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: 5}, Message: "m", Date: 10 + id})
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: msgs, Users: []tg.UserClass{&tg.User{ID: 5, AccessHash: 1}}}
		return nil
	}
}

func idsFrom(first, last, step int) []int {
	var ids []int
	for id := first; id <= last; id += step {
		ids = append(ids, id)
	}
	return ids
}

// Every page fetchAt can ask for, over chats of every size, claims a span
// that holds no message the page lacks, reaches the chat's end only when it
// does, and covers the page. A page of the newest messages, or of those
// after the anchor, also says when it reaches the end; the others may not
// know it.
func TestPageSpanMatrix(t *testing.T) {
	chats := map[string][]int{
		"empty":      nil,
		"one":        {7},
		"short":      idsFrom(1, 30, 1),
		"page":       idsFrom(1, 80, 1),
		"long":       idsFrom(1, 500, 1),
		"sparse":     idsFrom(1000, 9000, 37),
		"from 101":   idsFrom(101, 500, 1),
		"page and 1": idsFrom(1, 81, 1),
	}
	for name, ids := range chats {
		var anchors []int
		anchors = append(anchors, 0, 1, 2, 10_000)
		for i := 0; i < len(ids); i += max(1, len(ids)/7) {
			anchors = append(anchors, ids[i], ids[i]+1, ids[i]-1)
		}
		if len(ids) > 0 {
			anchors = append(anchors, ids[len(ids)-1], ids[len(ids)-1]+1)
		}
		for _, limit := range []int{1, 2, 3, 80} {
			for _, dir := range []int{-1, 0, 1} {
				for _, anchor := range anchors {
					if anchor <= 0 || (dir != 0 && anchor == 0) {
						continue
					}
					checkPageSpan(t, name, ids, dir, anchor, limit)
				}
				if dir == 0 {
					checkPageSpan(t, name, ids, 0, 0, limit)
				}
			}
		}
	}
}

func checkPageSpan(t *testing.T, name string, ids []int, dir, anchor, limit int) {
	t.Helper()
	what := fmt.Sprintf("%s, dir %d, anchor %d, limit %d", name, dir, anchor, limit)
	page := historyPage(ids, historyRequest(&tg.InputPeerEmpty{}, dir, anchor, limit))
	low, high, end, ok := pageSpan(page, dir, anchor, limit)
	if !ok {
		// Nothing covered: only an empty page.
		if len(page) != 0 {
			t.Errorf("%s: no span of page %v (end %v)", what, page, end)
		}
		return
	}
	for _, id := range ids {
		if id >= low && id <= high && !slices.Contains(page, id) && id != anchor {
			t.Errorf("%s: span %d..%d holds %d, which page %v lacks", what, low, high, id, page)
			return
		}
	}
	for _, id := range page {
		if id < low || id > high {
			t.Errorf("%s: span %d..%d leaves out %d of the page", what, low, high, id)
			return
		}
	}
	if len(ids) > 0 {
		newest := ids[len(ids)-1]
		reached := len(page) > 0 && page[0] == newest
		newer := 0
		for _, id := range ids {
			if id > anchor {
				newer++
			}
		}
		// After anchor are fewer messages than the page holds: Telegram
		// moved its window down. With as many, it cannot tell.
		known := anchor == 0 || dir > 0 && newer < limit
		if end && !reached || !end && reached && known {
			t.Errorf("%s: end %v, span %d..%d, page %v", what, end, low, high, page)
		}
	}
	if dir != 0 && anchor != 0 && (low > anchor+1 || high < anchor-1) {
		t.Errorf("%s: span %d..%d does not touch the anchor", what, low, high)
	}
}

// historyIDs are the IDs of h, and whether they are all of want's from the
// first on, without gaps.
func historyIDs(h model.History) []int {
	var out []int
	for _, m := range h.Messages {
		out = append(out, int(m.Key.MessageID))
	}
	return out
}

func checkRun(t *testing.T, what string, got, chat []int) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s: no messages", what)
	}
	i := slices.Index(chat, got[0])
	if i < 0 || i+len(got) > len(chat) || !slices.Equal(chat[i:i+len(got)], got) {
		t.Fatalf("%s: %d messages %d..%d are not one run of the chat", what, len(got), got[0], got[len(got)-1])
	}
}

func cacheMessages(t *testing.T, s *Store, chat int64, ids []int) {
	t.Helper()
	var raw []tg.MessageClass
	for _, id := range ids {
		raw = append(raw, &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: chat}, Message: "m", Date: 10 + id})
	}
	if _, e := s.ingest(context.Background(), raw, false, 0); e != nil {
		t.Fatal(e)
	}
}

func pageOlder(t *testing.T, s *Store, chat int64) model.History {
	t.Helper()
	for i := 0; i < 50 && s.History(chat).HasOlder; i++ {
		s.LoadOlder(chat)
		waitHistory(t, s, chat)
	}
	return s.History(chat)
}

// The cache has a chat's 200 oldest messages, from long ago, and 300 came
// since. Opened, the chat shows its newest messages without the old ones
// next to them, and pages back over the 300 to the first message: before,
// it showed 421..500 right after 200, and never loaded 201..420.
func TestOpenAfterManyNewMessagesLeavesNoGap(t *testing.T) {
	for _, spans := range []bool{false, true} {
		t.Run(fmt.Sprint("spans ", spans), func(t *testing.T) {
			s := testStore(t)
			chat := int64(5)
			all := idsFrom(1, 500, 1)
			s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
			cacheMessages(t, s, chat, all[:200])
			if spans {
				// Read by this version: a span holds them.
				if _, e := s.history.cache.AddSpan(context.Background(), chat, 1, 200); e != nil {
					t.Fatal(e)
				}
			}
			s.history.top[chat] = 500
			s.history.api = tg.NewClient(telegram.InvokeFunc(serveIDs(all)))

			s.OpenChat(chat)
			h := waitHistory(t, s, chat)
			checkRun(t, "opened", historyIDs(h), all)
			if ids := historyIDs(h); ids[len(ids)-1] != 500 || !h.HasOlder {
				t.Fatalf("opened at %d, HasOlder %v", ids[len(ids)-1], h.HasOlder)
			}
			h = pageOlder(t, s, chat)
			checkRun(t, "paged back", historyIDs(h), all)
			if ids := historyIDs(h); ids[0] != 1 || len(ids) != 500 {
				t.Fatalf("paged back to %d, %d messages", ids[0], len(ids))
			}

			// Offline, the cache gives the whole history back.
			s.Disconnect()
			s.reveal(chat, 0, true)
			s.OpenChat(chat)
			h = waitHistory(t, s, chat)
			checkRun(t, "offline", historyIDs(h), all)
			h = pageOlder(t, s, chat)
			if ids := historyIDs(h); ids[0] != 1 || len(ids) != 500 {
				t.Fatalf("offline, paged back to %d, %d messages", ids[0], len(ids))
			}
		})
	}
}

// Offline, a chat read in two runs with a gap between them shows the newest
// run, and paging back stops at the gap instead of going over it.
func TestOfflineHistoryStopsAtAGap(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	ctx := context.Background()
	all := idsFrom(1, 500, 1)
	cacheMessages(t, s, chat, all[:200])
	cacheMessages(t, s, chat, all[420:])
	for _, r := range [][2]int{{1, 200}, {421, 500}} {
		if _, e := s.history.cache.AddSpan(ctx, chat, r[0], r[1]); e != nil {
			t.Fatal(e)
		}
	}
	s.OpenChat(chat)
	h := pageOlder(t, s, chat)
	checkRun(t, "offline", historyIDs(h), all)
	if ids := historyIDs(h); ids[0] != 421 || ids[len(ids)-1] != 500 {
		t.Fatalf("offline history %d..%d", ids[0], ids[len(ids)-1])
	}
}

func newMessage(chat int64, id int) tg.UpdatesClass {
	return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: chat}, Message: "new", Date: 10 + id}}}}
}

// New messages that come as updates join the span of a chat read to its
// end, until the updates say they missed some.
func TestNewMessagesExtendTheLiveSpan(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	ctx := context.Background()
	all := idsFrom(1, 100, 1)
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.top[chat] = 100
	s.history.api = tg.NewClient(telegram.InvokeFunc(serveIDs(all)))
	s.OpenChat(chat)
	waitHistory(t, s, chat)

	top := func() int {
		span, ok, _, e := s.history.cache.SpanOf(ctx, chat, 0)
		if e != nil || !ok {
			t.Fatalf("no span: %v", e)
		}
		return span.High
	}
	if got := top(); got != 100 {
		t.Fatalf("the span ends at %d", got)
	}
	for _, id := range []int{101, 102} {
		if e := s.Handle(ctx, newMessage(chat, id)); e != nil {
			t.Fatal(e)
		}
	}
	if got := top(); got != 102 {
		t.Fatalf("new messages took the span to %d", got)
	}
	// An edit is not a new message.
	if e := s.Handle(ctx, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &tg.Message{ID: 110, PeerID: &tg.PeerUser{UserID: chat}, Message: "x", Date: 120}}}}); e != nil {
		t.Fatal(e)
	}
	if got := top(); got != 102 {
		t.Fatalf("an edit took the span to %d", got)
	}

	// 103 is missed: the updates were too long.
	s.endLive(0)
	if e := s.Handle(ctx, newMessage(chat, 104)); e != nil {
		t.Fatal(e)
	}
	if got := top(); got != 102 {
		t.Fatalf("a message after missed ones took the span to %d", got)
	}

	// The dialog list says 104 is the newest, and the span does not reach
	// it: the chat stays as it is.
	if e := s.liveFromDialogs(ctx, map[int64]int{chat: 104}, s.history.liveEpoch); e != nil {
		t.Fatal(e)
	}
	if s.history.live[chat] {
		t.Fatal("a chat whose span does not reach its newest message is live")
	}
	if e := s.liveFromDialogs(ctx, map[int64]int{chat: 102}, s.history.liveEpoch); e != nil {
		t.Fatal(e)
	}
	if !s.history.live[chat] {
		t.Fatal("a chat whose span reaches its newest message is not live")
	}
}

// A channel the account is not in sends no updates: read to its end, it is
// not live.
func TestLeftChannelIsNotLive(t *testing.T) {
	s := testStore(t)
	chat := int64(-1000000000005)
	c := s.history
	c.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "channel", Rights: peerRights{Left: true}}
	c.startLive(chat, c.liveEpoch)
	if c.live[chat] {
		t.Fatal("a channel the account is not in is live")
	}
	c.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "channel"}
	c.startLive(chat, c.liveEpoch-1)
	if c.live[chat] {
		t.Fatal("a chat read before updates were missed is live")
	}
	c.startLive(chat, c.liveEpoch)
	if !c.live[chat] {
		t.Fatal("a channel read to its end is not live")
	}
}

// An edit of a message the open history has not loaded does not join it:
// it is from another part of the chat.
func TestEditOfAnUnloadedMessageStaysOut(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	ctx := context.Background()
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.top[chat] = 500
	s.history.api = tg.NewClient(telegram.InvokeFunc(serveIDs(idsFrom(1, 500, 1))))
	s.OpenChat(chat)
	waitHistory(t, s, chat)
	edit := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &tg.Message{ID: 50, PeerID: &tg.PeerUser{UserID: chat}, Message: "edited", Date: 60, EditDate: 70}}}}
	if e := s.Handle(ctx, edit); e != nil {
		t.Fatal(e)
	}
	checkRun(t, "after the edit", historyIDs(s.History(chat)), idsFrom(1, 500, 1))
}

// countHistory serves ids, and counts the requests for history.
func countHistory(ids []int, n *atomic.Int32) func(context.Context, bin.Encoder, bin.Decoder) error {
	serve := serveIDs(ids)
	return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetHistoryRequest); ok {
			n.Add(1)
		}
		return serve(ctx, in, out)
	}
}

// A chat the cache has whole, from its first message, opens with nothing
// older to load, online and offline: the first page from Telegram holds 80
// of its 150 messages, but the cache gave the rest, down to the first.
func TestWholeCachedChatHasNothingOlder(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	all := idsFrom(1, 150, 1)
	cacheMessages(t, s, chat, all)
	if _, e := s.history.cache.AddSpan(context.Background(), chat, 1, 150); e != nil {
		t.Fatal(e)
	}
	var requests atomic.Int32
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.top[chat] = 150
	s.history.api = tg.NewClient(telegram.InvokeFunc(countHistory(all, &requests)))
	s.OpenChat(chat)
	h := waitHistory(t, s, chat)
	checkRun(t, "opened", historyIDs(h), all)
	if ids := historyIDs(h); ids[0] != 1 || h.HasOlder {
		t.Fatalf("opened from %d, HasOlder %v", ids[0], h.HasOlder)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("%d requests", n)
	}

	s.Disconnect()
	s.reveal(chat, 0, true)
	s.OpenChat(chat)
	if h = waitHistory(t, s, chat); h.HasOlder {
		t.Fatal("offline, the whole chat has older messages")
	}

	// A chat whose span does not reach its start still has older ones.
	s2 := testStore(t)
	cacheMessages(t, s2, chat, all[100:])
	if _, e := s2.history.cache.AddSpan(context.Background(), chat, 101, 150); e != nil {
		t.Fatal(e)
	}
	s2.OpenChat(chat)
	if h = waitHistory(t, s2, chat); !h.HasOlder {
		t.Fatal("offline, a chat cached from its 101st message has nothing older")
	}
}

// After missed updates, only the chats on screen are read again; the
// others opened in the session are dropped, to be read when opened.
func TestResyncReadsOnlyChatsOnScreen(t *testing.T) {
	s := testStore(t)
	var requests atomic.Int32
	s.history.api = tg.NewClient(telegram.InvokeFunc(countHistory(idsFrom(1, 10, 1), &requests)))
	chats := []int64{5, 6, 7}
	for _, chat := range chats {
		s.history.peers[chat] = peerRecord{ID: chat, Hash: 1, Kind: "user"}
		s.OpenChat(chat)
		waitHistory(t, s, chat)
	}
	viewer := new(int)
	s.WatchChat(viewer, 6)
	requests.Store(0)

	s.resync(0)
	h := waitHistory(t, s, 6)
	if n := requests.Load(); n != 1 {
		t.Fatalf("%d chats read again; want the one on screen", n)
	}
	if len(h.Messages) == 0 {
		t.Fatal("the chat on screen lost its history")
	}
	s.history.mu.Lock()
	kept := len(s.history.histories)
	s.history.mu.Unlock()
	if kept != 1 {
		t.Fatalf("%d histories kept; want the one on screen", kept)
	}

	// A hidden chat opened again is read again.
	s.OpenChat(5)
	waitHistory(t, s, 5)
	if n := requests.Load(); n != 2 {
		t.Fatalf("%d requests after opening a dropped chat", n)
	}

	// One channel's gap reads only it.
	requests.Store(0)
	s.WatchChat(new(int), 5)
	s.resync(5)
	waitHistory(t, s, 5)
	if n := requests.Load(); n != 1 {
		t.Fatalf("%d chats read again for one", n)
	}
}
