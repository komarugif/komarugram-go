package ui

import (
	"testing"
	"time"

	"komarugram/internal/messenger/model"

	"gioui.org/layout"
	"gioui.org/op"
)

// leapStore is a measureStore whose history leaps to a window around a
// message at once (model.HistoryWindow), and counts its leaps.
type leapStore struct {
	*measureStore
	leaps int
}

func (s *leapStore) LoadAround(_ int64, id model.MessageID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leaps++
	s.from = max(0, int(id)-1-100)
	s.shown = min(len(s.all), int(id)-1+100)
	s.revision++
	return true
}

// Dragged far into the skeletons, the history leaps there rather than page
// to it: the view comes to the messages of its row at once, the rows
// passed over keep their room, and the history is as high as before.
func TestHistoryLeapsIntoTheSkeletons(t *testing.T) {
	var store *leapStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &leapStore{measureStore: &measureStore{reserveStore: &reserveStore{menuStore: s, revision: 1, shown: 20, all: reserveMessages(600, time.Unix(1000, 0))}}}
		return store
	})
	heights := layOut(h, store.all)
	store.mu.Lock()
	store.heights = heights
	store.mu.Unlock()
	p := h.page
	p.reserve, p.measures, p.env = reserve{}, nil, model.RenderEnvironment{}
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 2, 0, true
	for range 50 {
		h.frame()
		if len(p.below) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	total := p.heights.Total()
	viewed := func() model.MessageID {
		i := p.list.Position.First - len(p.above)
		if i < 0 || i >= len(p.messages) {
			return 0
		}
		return p.messages[i].Key.MessageID
	}
	for _, to := range []int{400, 100} {
		// The row of message to, as the scrollbar's thumb takes the view.
		row := -1
		for k, s := range p.above {
			if s.id == model.MessageID(to) {
				row = k
			}
		}
		for k, s := range p.below {
			if s.id == model.MessageID(to) {
				row = len(p.above) + len(p.messages) + k
			}
		}
		if row < 0 {
			t.Fatalf("message %d is no skeleton", to)
		}
		p.list.Position.First, p.list.Position.Offset = row, 0
		store.mu.Lock()
		leaps, newer := store.leaps, store.newer
		store.mu.Unlock()
		h.frames(4)
		store.mu.Lock()
		leapt, paged := store.leaps-leaps, store.newer-newer
		store.mu.Unlock()
		if leapt != 1 || paged != 0 || viewed() != model.MessageID(to) {
			t.Fatalf("to %d: %d leaps, %d pages, the view at message %d", to, leapt, paged, viewed())
		}
		if got := p.heights.Total(); got != total {
			t.Fatalf("to %d: the history was %d px high, %d after the leap", to, total, got)
		}
	}
}

// The ring of a load shows only once loads went on for a while, and stays
// through the short gaps between pages: pages from the cache, each a few
// tens of milliseconds, made it blink.
func TestLoadRingWaitsAndHolds(t *testing.T) {
	p := &chatPage{}
	now := time.Unix(1000, 0)
	ring := func(loading bool) bool {
		gtx := layout.Context{Ops: new(op.Ops), Now: now}
		shown := p.loadRing(gtx, loading)
		now = now.Add(16 * time.Millisecond)
		return shown
	}
	// A page alone, within the delay: no ring.
	for range 3 {
		if ring(true) {
			t.Fatal("the ring showed for a short load")
		}
	}
	for range 20 {
		ring(false)
	}
	// Pages one after another, loading and not by turns: the ring comes
	// after the delay, and does not blink.
	shownAt := -1
	for i := range 60 {
		if ring(i%4 != 3) {
			if shownAt < 0 {
				shownAt = i
			}
		} else if shownAt >= 0 {
			t.Fatalf("the ring went at frame %d, between pages", i)
		}
	}
	if shownAt < 0 || time.Duration(shownAt)*16*time.Millisecond < ringDelay {
		t.Fatalf("the ring showed at frame %d", shownAt)
	}
	// Loads over: it goes after the gap.
	gone := false
	for range 30 {
		if !ring(false) {
			gone = true
			break
		}
	}
	if !gone {
		t.Fatal("the ring stayed after the loads")
	}
}

// The button to the start stays while pages of older messages load, as
// they do one after another in a scroll up: hidden while each came, it
// blinked, and took the ring above it up and down. A jump to the start on
// its way hides it.
func TestToStartStaysWhileOlderLoad(t *testing.T) {
	p := &chatPage{}
	history := model.History{HasOlder: true, HasNewer: true}
	if !p.showsToStart(history, 0) {
		t.Fatal("no button to the start, with older messages to go to")
	}
	history.LoadingOlder = true
	if !p.showsToStart(history, 0) {
		t.Fatal("the button to the start went while older messages loaded")
	}
	p.jump = jumpStart
	if p.showsToStart(history, 0) {
		t.Fatal("the button to the start stayed while a jump to it was on its way")
	}
}
