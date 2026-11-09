package ui

import (
	"fmt"
	"image"
	"os"
	"sync"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// reserveStore has a chat of messages, of which it shows a window, pages
// of newer ones coming on LoadNewer, and tells the rest by their heights
// (model.HistoryReserve).
type reserveStore struct {
	*menuStore
	mu       sync.Mutex
	all      []model.Message
	from     int
	shown    int
	revision uint64
	heights  map[model.MessageID]int
	asked    int
	newer    int
	older    int
}

func (s *reserveStore) history() model.History {
	return model.History{Messages: append([]model.Message(nil), s.all[s.from:s.shown]...), Revision: s.revision, HasNewer: s.shown < len(s.all), HasOlder: s.from > 0}
}

func (s *reserveStore) HistorySince(_ int64, revision uint64) (model.History, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.history()
	if revision == h.Revision {
		h.Messages = nil
		return h, false
	}
	return h, true
}

func (s *reserveStore) History(int64) model.History {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.history()
}

func (s *reserveStore) LoadNewer(int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.newer++
	s.shown = min(len(s.all), s.shown+20)
	s.revision++
}

func (s *reserveStore) LoadOlder(int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.older++
}

func (s *reserveStore) Reserve(_ int64, after model.MessageID, env model.RenderEnvironment, _ byte, limit int) []model.ReserveRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked++
	var rows []model.ReserveRow
	for _, m := range s.all {
		if m.Key.MessageID <= after || len(rows) == limit {
			continue
		}
		h, ok := s.heights[m.Key.MessageID]
		if !ok {
			h = -1
		}
		rows = append(rows, model.ReserveRow{ID: m.Key.MessageID, Revision: m.ContentRevision, Height: h, HeightRevision: m.ContentRevision})
	}
	return rows
}

// reserveMessages are a chat's messages, each its own sender's, so that no
// two join, and their rows are as high in any order.
func reserveMessages(n int, now time.Time) []model.Message {
	var msgs []model.Message
	for i := 1; i <= n; i++ {
		text := fmt.Sprintf("Message %d", i)
		if i%3 == 0 {
			text += " with a longer text that wraps onto a second line of the bubble, and a third"
		}
		msgs = append(msgs, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}, Date: now, Text: text, SenderName: "Ann", SenderID: int64(1000 + i), Outgoing: i%4 == 0, ContentRevision: 1})
	}
	return msgs
}

// Below where it opened, the history reserves the room of the rows the
// cache keeps: the scrollbar reaches as far as they do, and they come in,
// page by page, as high as they were reserved, the view staying where it
// is.
func TestHistoryReservesTheRowsBelow(t *testing.T) {
	var store *reserveStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		// The harness draws from its start: at time.Unix(1000, 0).
		store = &reserveStore{menuStore: s, revision: 1, shown: 20, all: reserveMessages(60, time.Unix(1000, 0))}
		return store
	})
	// The rows' heights, as the history lays them out at its width.
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	heights := map[model.MessageID]int{}
	for _, m := range store.all {
		gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(500, 1<<20)}, Metric: metric, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		heights[m.Key.MessageID] = h.page.row(gtx, m, m.Key.MessageID == 1, 0, localization.For("en"), false).Size.Y
	}
	store.mu.Lock()
	store.heights = heights
	store.mu.Unlock()
	p := h.page
	// Opened again, now that the cache knows the rows' heights.
	p.reserve = reserve{}
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 5, 0, true
	for range 50 {
		h.frame()
		if len(p.below) > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	loaded := len(p.messages)
	if len(p.below) != 60-loaded || p.below[0].id != model.MessageID(loaded+1) {
		t.Fatalf("%d rows reserved below %d of 60", len(p.below), loaded)
	}
	reserved := 0
	for id := model.MessageID(loaded + 1); id <= 60; id++ {
		reserved += heights[id]
	}
	below := func() int { return int(p.heights.Total()-p.heights.Prefix(len(p.messages))) - p.tail }
	if got := below(); got != reserved {
		t.Fatalf("the reserve below takes %d px, its rows %d", got, reserved)
	}
	// Scrolled into the reserve: its pages come, and the rows in them are
	// as high as the skeletons were. Rows loaded before the reserve came
	// are laid out off screen, and the history is as high as its rows.
	into := loaded + 10
	p.list.Position.First, p.list.Position.Offset = into, 0
	total := int64(p.tail)
	for _, height := range heights {
		total += int64(height)
	}
	for range 40 {
		h.frame()
	}
	store.mu.Lock()
	newer, shown := store.newer, store.shown
	store.mu.Unlock()
	if newer == 0 || shown <= into {
		t.Fatalf("scrolled into the reserve, %d pages asked for, %d messages shown", newer, shown)
	}
	if got := p.heights.Total(); got != total {
		t.Fatalf("the history was %d px high, %d once pages came", total, got)
	}
	if id := p.messages[p.list.Position.First].Key.MessageID; id != model.MessageID(into+1) {
		t.Fatalf("the view went to message %d from %d", id, into+1)
	}
}

// Opened at its end, the history has nothing below it to reserve, and does
// not ask.
func TestHistoryAtItsEndReservesNothing(t *testing.T) {
	var store *reserveStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &reserveStore{menuStore: s, revision: 1, shown: 20, all: reserveMessages(20, time.Unix(1000, 0))}
		return store
	})
	h.frames(10)
	store.mu.Lock()
	asked := store.asked
	store.mu.Unlock()
	if asked != 0 || len(h.page.below) != 0 {
		t.Fatalf("at its end the history asked for a reserve %d times, has %d rows of it", asked, len(h.page.below))
	}
}

// The reserve is the rows the cache keeps as far as their heights are
// known: an album is its first part's row, and a row of another revision,
// or of no height and no profile, ends it.
func TestReserveRowsEndAtOneNotKnown(t *testing.T) {
	env := model.RenderEnvironment{WidthPx: 500, ScaleMilli: 1000, TextScaleMilli: 1000}
	rows := []model.ReserveRow{
		{ID: 1, Revision: 1, Height: 40, HeightRevision: 1},
		{ID: 2, Revision: 7, GroupedID: 9, Media: true, Height: 300, HeightRevision: 5},
		{ID: 3, Revision: 2, GroupedID: 9, Media: true, Height: -1},
		{ID: 4, Revision: 1, Height: 50, HeightRevision: 1, Outgoing: true},
		{ID: 5, Revision: 2, Height: 60, HeightRevision: 1},
		{ID: 6, Revision: 1, Height: 70, HeightRevision: 1},
	}
	got := reserveRows(rows, env)
	var ids []model.MessageID
	for _, s := range got {
		ids = append(ids, s.id)
	}
	if fmt.Sprint(ids) != "[1 2 4]" || got[1].height != 300 || !got[2].outgoing {
		t.Fatalf("the reserve is %+v", got)
	}
}

// Older messages that come above, while the view is in the reserve, keep
// it at the row it is at.
func TestReservedRowKeepsTheView(t *testing.T) {
	var store *reserveStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &reserveStore{menuStore: s, revision: 1, from: 20, shown: 40, all: reserveMessages(200, time.Unix(1000, 0))}
		return store
	})
	heights := map[model.MessageID]int{}
	for _, m := range store.all {
		heights[m.Key.MessageID] = 60
	}
	store.mu.Lock()
	store.heights = heights
	store.mu.Unlock()
	p := h.page
	p.reserve = reserve{}
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 5, 0, true
	for range 50 {
		h.frame()
		if len(p.below) > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(p.below) < 10 {
		t.Fatalf("%d rows reserved", len(p.below))
	}
	// Deep in the reserve, where no page has come yet.
	at := p.below[len(p.below)/2]
	p.list.Position.First, p.list.Position.Offset = len(p.messages)+len(p.below)/2, 7
	store.mu.Lock()
	store.from -= 10
	store.revision++
	store.mu.Unlock()
	h.frame()
	first := p.list.Position.First
	if first < len(p.messages) || p.below[first-len(p.messages)].id != at.id || p.list.Position.Offset != 7 {
		t.Fatalf("older messages came above, and the view went from row %d+7 to %d+%d", at.id, first, p.list.Position.Offset)
	}
}

// TestRenderReserve saves the history with skeletons of its reserve below
// what it loaded, for looking at them: RESERVE_PNG=/tmp/reserve.png.
func TestRenderReserve(t *testing.T) {
	path := os.Getenv("RESERVE_PNG")
	if path == "" {
		t.Skip("set RESERVE_PNG to a file")
	}
	messages := reserveMessages(12, time.Unix(1700000000, 0))
	var calls []string
	p := newChatPage(jumpSource{benchmarkHistory{h: model.History{Messages: messages, Revision: 1, HasNewer: true}}, &calls, nil}, func() {})
	defer p.Close()
	var rows []skeletonRow
	for i := 13; i <= 40; i++ {
		s := skeletonRow{id: model.MessageID(i), outgoing: i%4 == 0, height: 56 + (i%3)*21, bubble: image.Pt(120+(i*37)%260, 48+(i%3)*21)}
		if i%9 == 0 {
			s = skeletonRow{id: model.MessageID(i), service: true, height: 40, bubble: image.Pt(180, 26)}
		}
		rows = append(rows, s)
	}
	frames := 0
	renderFrames(t, image.Pt(500, 800), path, func(gtx layout.Context) {
		if frames == 1 {
			// As the work on the reserve hands it over, once the chat is open.
			p.reserve = reserve{chat: 1, env: p.env, asked: true}
			p.reserveDone = make(chan reserveResult, 1)
			tellReserve(p.reserveDone, reserveResult{chat: 1, env: p.env, rows: rows})
			p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 8, 0, true
		}
		frames++
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, localization.For("ru"), false)
	})
}

// A row of the reserve whose height was not kept at the history's width is
// as high as its profile, taken at another, tells: as its layout at it.
func TestReserveRowsFromProfiles(t *testing.T) {
	h := newMenuHarness(t, nil)
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	lay := func(m model.Message, width int) (*messageRow, int) {
		gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(width, 1<<20)}, Metric: metric, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		return h.page.rows[m.Key.MessageID], h.page.row(gtx, m, false, 0, localization.For("en"), false).Size.Y
	}
	var rows []model.ReserveRow
	msgs := predictRows(5001)[:12]
	for _, m := range msgs {
		lay(m, 600)
		r, height := lay(m, 600)
		data := h.page.takeProfile(r, m, 600, height)
		if data == nil {
			continue
		}
		rows = append(rows, model.ReserveRow{ID: m.Key.MessageID, Revision: m.ContentRevision, Outgoing: m.Outgoing, Height: -1, Profile: data, ProfileRevision: m.ContentRevision})
	}
	env := model.RenderEnvironment{WidthPx: 380, ScaleMilli: 1000, TextScaleMilli: 1000}
	got := reserveRows(rows, env)
	if len(got) != len(rows) {
		t.Fatalf("%d of %d rows reserved", len(got), len(rows))
	}
	for i, s := range got {
		m := msgs[i]
		if _, want := lay(m, 380); s.height != want || s.outgoing != m.Outgoing {
			t.Errorf("message %d: reserved %d px high, laid out %d", m.Key.MessageID, s.height, want)
		}
	}
}
