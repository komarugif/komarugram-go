package ui

import (
	"image"
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

// measureStore is a reserveStore whose cache keeps the whole chat
// (model.CachedHistory), with the heights it knows as layouts, and the
// messages replies name that it has without Telegram; it counts the
// lookups the history asks of Telegram.
type measureStore struct {
	*reserveStore
	lmu     sync.Mutex
	targets map[model.MessageID]model.Message
	lookups int
	saved   map[model.MessageID]int
}

// LoadNewer brings no page: the rows below stay the reserve's.
func (s *measureStore) LoadNewer(int64) {}

func (s *measureStore) CachedFrom(_ int64, from model.MessageID, limit int) []model.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Message
	for _, m := range s.all {
		if m.Key.MessageID >= from && len(out) < limit {
			out = append(out, m)
		}
	}
	return out
}

func (s *measureStore) CachedLookup(_ int64, id model.MessageID) (model.Message, model.LookupState, bool) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	m, ok := s.targets[id]
	if !ok {
		return model.Message{}, model.LookupLoading, false
	}
	return m, model.LookupFound, true
}

func (s *measureStore) LookupMessage(_ int64, id model.MessageID) (model.Message, model.LookupState) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	s.lookups++
	if m, ok := s.targets[id]; ok {
		return m, model.LookupFound
	}
	return model.Message{}, model.LookupLoading
}

func (s *measureStore) Layouts(_ int64, env model.RenderEnvironment) []model.MessageLayout {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.MessageLayout
	for _, m := range s.all {
		if h, ok := s.heights[m.Key.MessageID]; ok {
			out = append(out, model.MessageLayout{Key: m.Key, Environment: env, ContentRevision: m.ContentRevision, HeightPx: h})
		}
	}
	return out
}

func (s *measureStore) SaveView(_ model.Viewport, ls []model.MessageLayout) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	if s.saved == nil {
		s.saved = map[model.MessageID]int{}
	}
	for _, l := range ls {
		s.saved[l.Key.MessageID] = l.HeightPx
	}
}

// layOut is how high the rows of msgs are as the history lays them out at
// 500 px, the first with its date.
func layOut(h *menuHarness, msgs []model.Message) map[model.MessageID]int {
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	heights := map[model.MessageID]int{}
	for _, m := range msgs {
		gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(500, 1<<20)}, Metric: metric, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		heights[m.Key.MessageID] = h.page.row(gtx, m, m.Key.MessageID == 1, 0, localization.For("en"), false).Size.Y
	}
	return heights
}

// A run of rows the scroll went past, no height kept of them, ends the
// reserve; the list standing still, the history lays them out off screen
// from what the cache keeps, as high as a frame lays them out, and the
// reserve grows past them to the chat's end. Their replies name messages
// from the cache, never looked up on Telegram: a row whose reply names one
// the cache lacks is reserved, its height not kept.
func TestMeasurerGrowsTheReserveOverAGap(t *testing.T) {
	var store *measureStore
	all := reserveMessages(60, time.Unix(1000, 0))
	all[32].ReplyToMessageID = 5
	all[35].ReplyToMessageID = 99
	all[37].ReplyToMessageID = 98
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &measureStore{reserveStore: &reserveStore{menuStore: s, revision: 1, shown: 20, all: all}}
		store.targets = map[model.MessageID]model.Message{99: {Key: model.MessageKey{ChatID: 1, MessageID: 99}, Text: "A message of long ago, kept in the cache", SenderName: "Bob", SenderID: 7}}
		return store
	})
	heights := layOut(h, store.all)
	known := map[model.MessageID]int{}
	for id, height := range heights {
		if id < 30 || id > 45 {
			known[id] = height
		}
	}
	store.mu.Lock()
	store.heights = known
	store.mu.Unlock()
	store.lmu.Lock()
	store.lookups = 0
	store.lmu.Unlock()
	p := h.page
	// Opened again, now that the cache knows the rows' heights but those of
	// 30 to 45.
	p.reserve, p.measures, p.env = reserve{}, nil, model.RenderEnvironment{}
	p.resetMeasurer()
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 2, 0, true
	laidOut := p.measurer.laidOut
	end := func() model.MessageID {
		if len(p.below) == 0 {
			return 0
		}
		return p.below[len(p.below)-1].id
	}
	for range 300 {
		h.frame()
		if end() == 60 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if end() != 60 {
		t.Fatalf("the reserve ends at %d, %d rows below %d loaded", end(), len(p.below), len(p.messages))
	}
	last := p.messages[len(p.messages)-1].Key.MessageID
	for i, s := range p.below {
		if want := last + model.MessageID(i) + 1; s.id != want {
			t.Fatalf("row %d of the reserve is message %d, not %d", i, s.id, want)
		}
		if s.height != heights[s.id] {
			t.Errorf("message %d reserved %d px high, laid out %d", s.id, s.height, heights[s.id])
		}
	}
	// The rows past the gap, of heights kept, came from the cache.
	if n := p.measurer.laidOut - laidOut; n != 16 {
		t.Errorf("%d rows laid out off screen, of the 16 of no height kept", n)
	}
	// Saved as the history saves where it is, every half a second.
	p.save(true)
	store.lmu.Lock()
	lookups, saved := store.lookups, store.saved
	store.lmu.Unlock()
	if lookups != 0 {
		t.Errorf("the rows laid out off screen looked %d messages up on Telegram", lookups)
	}
	for id := model.MessageID(30); id <= 45; id++ {
		h, ok := saved[id]
		if id == 38 {
			if ok {
				t.Errorf("message 38, replying to one the cache lacks, kept %d px high", h)
			}
			continue
		}
		if !ok || h != heights[id] {
			t.Errorf("message %d kept %d px high (%t), laid out %d", id, h, ok, heights[id])
		}
	}
	for id := range p.rows {
		if id > last {
			t.Fatalf("the row of message %d, not loaded, stayed", id)
		}
	}
}

// The loaded rows no frame laid out are laid out off screen while the list
// stands still, their media not loaded for it.
func TestMeasurerLaysOutLoadedRows(t *testing.T) {
	all := reserveMessages(60, time.Unix(1000, 0))
	// Shown by no frame: the history opens at its end, then goes to its top.
	all[29].Kind, all[29].Media = model.MessagePhoto, &model.MessageMedia{ID: "photo/30", Width: 800, Height: 600}
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		return &reserveStore{menuStore: s, revision: 1, shown: 60, all: all}
	})
	p := h.page
	entries, _ := p.media.Stats()
	if _, ok := p.measures[30]; ok {
		t.Fatal("the photo's row, off screen, was laid out before the list stood still")
	}
	h.frames(20)
	for _, m := range p.messages {
		if l, ok := p.measures[m.Key.MessageID]; !ok || l.ContentRevision != m.ContentRevision {
			t.Fatalf("message %d was not laid out", m.Key.MessageID)
		}
	}
	if got, _ := p.media.Stats(); got != entries {
		t.Fatalf("laid out off screen, the rows asked for %d media", got-entries)
	}
}
