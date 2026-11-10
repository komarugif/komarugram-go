package tgstore

import (
	"context"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

func windowHistory(n int) *model.History {
	h := &model.History{}
	for i := 1; i <= n; i++ {
		h.Messages = append(h.Messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}})
	}
	return h
}

// A page that comes at one end lets the messages at the other go past the
// window, marking that end as having more; never near the message viewed,
// nor through an album.
func TestWindowLetsTheFarEndGo(t *testing.T) {
	c := &conversation{views: map[int64]model.Viewport{}}
	h := windowHistory(historyWindow + 100)
	c.trimWindow(1, h, 1)
	if len(h.Messages) != historyWindow || h.Messages[0].Key.MessageID != 101 || !h.HasOlder || h.HasNewer {
		t.Fatalf("a newer page: %d kept from %d, older %t, newer %t", len(h.Messages), h.Messages[0].Key.MessageID, h.HasOlder, h.HasNewer)
	}

	h = windowHistory(historyWindow + 100)
	c.trimWindow(1, h, -1)
	if last := h.Messages[len(h.Messages)-1].Key.MessageID; len(h.Messages) != historyWindow || last != historyWindow || !h.HasNewer || h.HasOlder {
		t.Fatalf("an older page: %d kept up to %d, newer %t", len(h.Messages), last, h.HasNewer)
	}

	// The view near the end let go of: the margin past it stays.
	h = windowHistory(historyWindow + 100)
	c.views[1] = model.Viewport{ChatID: 1, AnchorMessageID: 180}
	c.trimWindow(1, h, 1)
	if first := h.Messages[0].Key.MessageID; first != 180-windowMargin {
		t.Fatalf("viewed at 180, the window begins at %d", first)
	}

	// An album across the cut stays whole.
	h = windowHistory(historyWindow + 100)
	for _, i := range []int{98, 99, 100, 101, 102} {
		h.Messages[i].GroupedID, h.Messages[i].Media = 7, &model.MessageMedia{ID: "p"}
	}
	delete(c.views, 1)
	c.trimWindow(1, h, 1)
	if first := h.Messages[0].Key.MessageID; first != 99 {
		t.Fatalf("the window begins at %d, in the album of 99..103", first)
	}

	// Within the window, nothing goes.
	h = windowHistory(historyWindow)
	c.trimWindow(1, h, 1)
	if len(h.Messages) != historyWindow || h.HasOlder {
		t.Fatal("a history within the window lost messages")
	}
}

// Pages that come keep the history a window: the newer that come let the
// oldest go, and the history has older ones again.
func TestPagesKeepTheHistoryAWindow(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	page := func(from, n int) []model.Message {
		var out []model.Message
		for i := from; i < from+n; i++ {
			out = append(out, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(i)}})
		}
		return out
	}
	s.history.mu.Lock()
	s.history.histories[chat] = &model.History{Messages: page(1, 100)}
	s.history.top[chat] = 10000
	epoch := s.history.epochs[chat]
	s.history.mu.Unlock()
	for from := 101; from < 1000; from += 100 {
		s.finishPageAt(epoch, chat, 1, page(from, 100), true, nil)
	}
	h := s.History(chat)
	if len(h.Messages) > historyWindow || h.Messages[len(h.Messages)-1].Key.MessageID != 1000 || !h.HasOlder || !h.HasNewer {
		t.Fatalf("after 10 pages: %d messages, %d..%d, older %t", len(h.Messages), h.Messages[0].Key.MessageID, h.Messages[len(h.Messages)-1].Key.MessageID, h.HasOlder)
	}
}

// A leap brings the window around the message from the cache at once, the
// history going on both ways from it.
func TestLeapLoadsAroundTheMessage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	chat := int64(5)
	var all []model.Message
	for i := 1; i <= 1000; i++ {
		all = append(all, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(i)}, Text: "m"})
	}
	cache := s.history.cache
	if err := cache.SaveMessages(ctx, all); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.AddSpan(ctx, chat, 1, 1000); err != nil {
		t.Fatal(err)
	}
	s.history.mu.Lock()
	s.history.histories[chat] = &model.History{Messages: all[:100], HasNewer: true}
	s.history.top[chat], s.history.firsts[chat] = 1000, 1
	s.history.mu.Unlock()
	if !s.LoadAround(chat, 500) {
		t.Fatal("the leap did not start")
	}
	var h model.History
	for range 200 {
		if h = s.History(chat); !h.LoadingNewer && !h.LoadingOlder {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(h.Messages) == 0 || h.Messages[0].Key.MessageID > 500 || h.Messages[len(h.Messages)-1].Key.MessageID < 500 || !h.HasOlder || !h.HasNewer {
		t.Fatalf("after the leap: %d messages, older %t, newer %t", len(h.Messages), h.HasOlder, h.HasNewer)
	}
	if len(h.Messages) > 2*leapWindow {
		t.Fatalf("the leap brought %d messages", len(h.Messages))
	}
}
