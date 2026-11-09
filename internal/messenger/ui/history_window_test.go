package ui

import (
	"image"
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

// The store lets go of loaded messages far from the view, above and below:
// their rows keep their room, as skeletons, the view staying at its row and
// the scrollbar where it was; the messages that come again take their place
// as high as they were.
func TestLetGoRowsKeepTheirRoom(t *testing.T) {
	var store *reserveStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &reserveStore{menuStore: s, revision: 1, shown: 80, all: reserveMessages(200, time.Unix(1000, 0))}
		return store
	})
	p := h.page
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 50, 7, true
	h.frames(60)
	type view struct {
		id       model.MessageID
		at, all  int64
		above, n int
	}
	look := func() view {
		pos := p.list.Position
		i := pos.First - len(p.above)
		if i < 0 || i >= len(p.messages) {
			t.Fatalf("the view is on row %d, not a loaded message", pos.First)
		}
		return view{p.messages[i].Key.MessageID, p.heights.Prefix(pos.First) + int64(pos.Offset), p.heights.Total(), len(p.above), len(p.messages)}
	}
	change := func(from, shown int) view {
		store.mu.Lock()
		store.from, store.shown = from, shown
		store.revision++
		store.mu.Unlock()
		h.frames(3)
		return look()
	}
	before := look()
	steps := []struct {
		name        string
		from, shown int
		above       int
	}{
		{"the oldest 30 let go", 30, 80, 30},
		{"the newest 10 let go", 30, 70, 30},
		{"20 of the oldest back", 10, 70, 10},
		{"all back", 0, 80, 0},
	}
	for _, s := range steps {
		got := change(s.from, s.shown)
		if got.id != before.id || got.at != before.at || got.all != before.all || got.above != s.above {
			t.Fatalf("%s: the view is %+v, was %+v", s.name, got, before)
		}
	}
	// Scrolled into the rows let go of above: their pages are asked for.
	change(30, 80)
	p.list.Position.First, p.list.Position.Offset = 5, 0
	h.frames(3)
	store.mu.Lock()
	asked := store.older
	store.mu.Unlock()
	if asked == 0 {
		t.Fatal("in the rows let go of above, no older page was asked for")
	}
}

// An album the window cuts is keyed by another part once the page before
// it comes again: the rows let go of above stay, the view at its row.
func TestRoomAboveOutlivesACutAlbum(t *testing.T) {
	var store *reserveStore
	all := reserveMessages(200, time.Unix(1000, 0))
	for _, i := range []int{28, 29, 30} {
		all[i].GroupedID, all[i].Kind, all[i].Media = 9, model.MessagePhoto, &model.MessageMedia{ID: "photo/album", Width: 800, Height: 600}
		all[i].SenderID = 1029
	}
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &reserveStore{menuStore: s, revision: 1, shown: 80, all: all}
		return store
	})
	p := h.page
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 50, 0, true
	h.frames(30)
	change := func(from int) {
		store.mu.Lock()
		store.from = from
		store.revision++
		store.mu.Unlock()
		h.frames(3)
	}
	viewed := func() model.MessageID {
		i := p.list.Position.First - len(p.above)
		if i < 0 || i >= len(p.messages) {
			t.Fatalf("the view is on row %d, not a loaded message", p.list.Position.First)
		}
		return p.messages[i].Key.MessageID
	}
	before := viewed()
	// The window begins inside the album, at its second part.
	change(29)
	if len(p.above) != 28 || viewed() != before {
		t.Fatalf("let go to the album: %d rows above, the view at %d, was %d", len(p.above), viewed(), before)
	}
	// The album whole again, keyed by its first part.
	change(20)
	if len(p.above) != 20 || viewed() != before {
		t.Fatalf("the album whole again: %d rows above, the view at %d, was %d", len(p.above), viewed(), before)
	}
}

// Rows of the reserve loaded and let go of again before any frame laid
// them out keep the bubbles their skeletons had: they did not all become
// skeletons of one width.
func TestLetGoRowsKeepTheirShapes(t *testing.T) {
	var store *measureStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &measureStore{reserveStore: &reserveStore{menuStore: s, revision: 1, shown: 20, all: reserveMessages(200, time.Unix(1000, 0))}}
		return store
	})
	heights := layOut(h, store.all)
	store.mu.Lock()
	store.heights = heights
	store.mu.Unlock()
	p := h.page
	// Opened again: every height known, and the reserve with the shapes of
	// its bubbles.
	p.reserve, p.measures, p.env = reserve{}, nil, model.RenderEnvironment{}
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 2, 0, true
	h.frames(3)
	shape := func(id model.MessageID) image.Point { return image.Pt(100+int(id), 30) }
	var rows []skeletonRow
	for id := model.MessageID(21); id <= 200; id++ {
		rows = append(rows, skeletonRow{id: id, height: heights[id], bubble: shape(id)})
	}
	p.reserve = reserve{chat: 1, env: p.env, asked: true}
	p.reserveDone = make(chan reserveResult, 1)
	tellReserve(p.reserveDone, reserveResult{chat: 1, env: p.env, rows: rows})
	h.frames(3)
	// Loaded, out of view, then let go of again.
	for _, shown := range []int{80, 30} {
		store.mu.Lock()
		store.shown = shown
		store.revision++
		store.mu.Unlock()
		h.frames(3)
	}
	if len(p.below) == 0 {
		t.Fatal("nothing reserved below")
	}
	checked := 0
	for _, s := range p.below {
		if s.id > 80 || p.rows[s.id] != nil {
			// The reserve's own, or laid out by a frame: its own shape.
			continue
		}
		checked++
		if s.bubble != shape(s.id) {
			t.Fatalf("message %d let go of has a bubble of %v, its skeleton had %v", s.id, s.bubble, shape(s.id))
		}
	}
	if checked < 30 {
		t.Fatalf("only %d rows let go of before a frame laid them out", checked)
	}
}

// shapeStore is a measureStore whose cache keeps the rows' profiles,
// which its reserve tells.
type shapeStore struct {
	*measureStore
	profiles map[model.MessageID][]byte
}

func (s *shapeStore) ProfilesBetween(_ int64, _ model.RenderEnvironment, _ byte, low, high model.MessageID) map[model.MessageID][]byte {
	out := map[model.MessageID][]byte{}
	for id, data := range s.profiles {
		if id >= low && id <= high {
			out[id] = data
		}
	}
	return out
}

func (s *shapeStore) Reserve(chat int64, after model.MessageID, env model.RenderEnvironment, version byte, limit int) []model.ReserveRow {
	rows := s.measureStore.Reserve(chat, after, env, version, limit)
	for i := range rows {
		if data, ok := s.profiles[rows[i].ID]; ok {
			rows[i].Profile, rows[i].ProfileRevision = data, rows[i].Revision
		}
	}
	return rows
}

// Rows loaded above where the chat opened, let go of before a frame laid
// them out, get the bubbles their profiles in the cache tell: as those of
// the reserve, not one width for all.
func TestLetGoRowsTakeShapesFromProfiles(t *testing.T) {
	var store *shapeStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &shapeStore{measureStore: &measureStore{reserveStore: &reserveStore{menuStore: s, revision: 1, shown: 80, all: reserveMessages(200, time.Unix(1000, 0))}}}
		return store
	})
	heights := layOut(h, store.all)
	// The rows' profiles, laid out at the history's width.
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	profiles := map[model.MessageID][]byte{}
	bubbles := map[model.MessageID]int{}
	for _, m := range store.all[:60] {
		var r *messageRow
		var height int
		for range 2 {
			gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(500, 1<<20)}, Metric: metric, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, theme)
			height = h.page.row(gtx, m, m.Key.MessageID == 1, 0, localization.For("en"), false).Size.Y
			r = h.page.rows[m.Key.MessageID]
		}
		if data := h.page.takeProfile(r, m, 500, height); data != nil {
			profiles[m.Key.MessageID] = data
			bubbles[m.Key.MessageID] = r.bodySize.X
		}
	}
	store.mu.Lock()
	store.heights = heights
	store.profiles = profiles
	store.mu.Unlock()
	p := h.page
	// Opened again with every height known, far below the rows that go.
	p.reserve, p.measures, p.env, p.rows = reserve{}, nil, model.RenderEnvironment{}, map[model.MessageID]*messageRow{}
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 70, 0, true
	h.frames(3)
	store.mu.Lock()
	store.from = 40
	store.revision++
	store.mu.Unlock()
	h.frames(3)
	for range 50 {
		h.frame()
		if len(p.above) > 0 && p.above[len(p.above)-1].profile != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(p.above) != 40 {
		t.Fatalf("%d rows above", len(p.above))
	}
	checked := 0
	for _, s := range p.above {
		want, ok := bubbles[s.id]
		if !ok {
			continue
		}
		checked++
		// Its bubble, as the skeleton works it out when it is drawn.
		shape, _ := p.shapeOf(s, false)
		if shape.bubble.X != want {
			t.Fatalf("message %d let go of above has a bubble %d wide, laid out %d", s.id, shape.bubble.X, want)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d rows above had profiles", checked)
	}
}
