// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/pkg/audio"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/op"
)

// sharedHarness lays the info of the demo's chat 2 out in a window of
// 1000 by 800, its dialog of 640 by 740 in the middle, and takes the
// pointer and the keyboard to it.
type sharedHarness struct {
	t      *testing.T
	p      *chatInfo
	router input.Router
	now    time.Time
}

func newSharedHarness(t *testing.T, kind model.SharedKind) *sharedHarness {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	t.Cleanup(p.Destroy)
	h := &sharedHarness{t: t, p: p, now: time.Now()}
	p.Open(model.Chat{ID: 2, Title: "Анна", Kind: model.KindUser})
	awaitShared(t, p)
	p.selectSection(kind)
	awaitShared(t, p)
	for range 40 {
		h.frame()
	}
	return h
}

func (h *sharedHarness) frame() {
	ops := new(op.Ops)
	gtx := sharedContext(ops, image.Pt(1000, 800))
	gtx.Source, gtx.Now = h.router.Source(), h.now
	h.p.renderer.images.BeginFrame()
	h.p.Layout(gtx, localization.For("en"), false)
	h.p.renderer.images.EndFrame()
	h.router.Frame(ops)
	h.now = h.now.Add(20 * time.Millisecond)
}

func (h *sharedHarness) click(buttons pointer.Buttons, at f32.Point) {
	for _, kind := range []pointer.Kind{pointer.Move, pointer.Press, pointer.Release} {
		e := pointer.Event{Kind: kind, Source: pointer.Mouse, Position: at}
		if kind != pointer.Move {
			e.Buttons = buttons
		}
		h.router.Queue(e)
		h.frame()
	}
	for range 20 {
		h.frame()
	}
}

// chooseItem clicks a in the open menu.
func (h *sharedHarness) chooseItem(a sharedAction) {
	h.t.Helper()
	m := &h.p.sharedMenu
	i := slices.Index(m.shown, a)
	if !m.open || i < 0 {
		h.t.Fatalf("the menu, open %v, shows no %v: %v", m.open, a, m.shown)
	}
	b := m.menu.bounds
	h.click(pointer.ButtonPrimary, f32.Pt(float32(b.Min.X+40), float32(b.Min.Y+menuPadding+i*menuItemHeight+menuItemHeight/2)))
}

// The photos are a grid from (196, 114), four of about 150 a row.
func tileAt(i int) f32.Point {
	return f32.Pt(float32(196+75+i%4*153), float32(114+75+i/4*153))
}

// A right click on a thumbnail opens its menu, whose Select selects it;
// then a click selects or unselects a thumbnail instead of opening it,
// the header has the selection's buttons, and Escape clears it before it
// closes the dialog.
func TestSharedSelection(t *testing.T) {
	h := newSharedHarness(t, model.SharedPhotos)
	p := h.p
	var opened []model.MessageID
	p.renderer.openPhoto = func(m model.Message) { opened = append(opened, m.Key.MessageID) }
	p.openMessage = func(model.Message) {}
	first, second := p.messages[0], p.messages[1]

	h.click(pointer.ButtonSecondary, tileAt(0))
	if !p.sharedMenu.open || p.sharedMenu.id != first.Key.MessageID {
		t.Fatalf("menu open %v of %d", p.sharedMenu.open, p.sharedMenu.id)
	}
	if want := []sharedAction{sharedGoTo, sharedSave, sharedDelete, sharedSelect}; !slices.Equal(p.sharedMenu.shown, want) {
		t.Fatalf("the menu shows %v", p.sharedMenu.shown)
	}
	h.chooseItem(sharedSelect)
	if !p.selecting() || !p.renderer.selection.selected[first.Key.MessageID] {
		t.Fatalf("Select selected %v", p.renderer.selection.selected)
	}
	h.click(pointer.ButtonPrimary, tileAt(1))
	if !p.renderer.selection.selected[second.Key.MessageID] || len(opened) > 0 {
		t.Fatalf("a click selected %v, opened %v", p.renderer.selection.selected, opened)
	}
	if p.renderer.selectionCount() != 2 {
		t.Fatalf("the header counts %d", p.renderer.selectionCount())
	}
	h.click(pointer.ButtonPrimary, tileAt(1))
	if p.renderer.selection.selected[second.Key.MessageID] {
		t.Fatal("a second click left it selected")
	}

	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame()
	if p.selecting() || !p.modal.Shown() || p.section != model.SharedPhotos {
		t.Fatalf("Escape: selecting %v, shown %v, section %q", p.selecting(), p.modal.Shown(), p.section)
	}
	// Nothing selected, a click opens the photo.
	h.click(pointer.ButtonPrimary, tileAt(1))
	if len(opened) != 1 || opened[0] != second.Key.MessageID {
		t.Fatalf("opened %v", opened)
	}
}

// Delete in a file's menu deletes it, and the section lists it no more;
// Go To Message closes the dialog and goes to it.
func TestSharedMenuDeletesAndGoes(t *testing.T) {
	h := newSharedHarness(t, model.SharedPhotos)
	p := h.p
	var went []model.MessageID
	p.openMessage = func(m model.Message) { went = append(went, m.Key.MessageID) }
	first, total := p.messages[0], len(p.messages)

	h.click(pointer.ButtonSecondary, tileAt(0))
	h.chooseItem(sharedDelete)
	if !p.renderer.deletion.modal.Shown() || !slices.Equal(p.renderer.deletion.ids, []model.MessageID{first.Key.MessageID}) {
		t.Fatalf("delete asks for %v", p.renderer.deletion.ids)
	}
	p.renderer.deleteSelected(false)
	deadline := time.Now().Add(3 * time.Second)
	for len(p.messages) == total && time.Now().Before(deadline) {
		h.frame()
		time.Sleep(time.Millisecond)
	}
	if len(p.messages) != total-1 || slices.ContainsFunc(p.messages, func(m model.Message) bool { return m.Key == first.Key }) {
		t.Fatalf("%d of %d left", len(p.messages), total)
	}

	next := p.messages[0]
	for range 30 {
		h.frame()
	}
	h.click(pointer.ButtonSecondary, tileAt(0))
	h.chooseItem(sharedGoTo)
	if len(went) != 1 || went[0] != next.Key.MessageID || p.modal.Shown() && !p.modal.closing {
		t.Fatalf("went to %v, dialog shown %v", went, p.modal.Shown())
	}
}

// A user's bio is a card of the info, its links and mentions links to
// open, as a message's.
func TestInfoAbout(t *testing.T) {
	h := newSharedHarness(t, model.SharedPhotos)
	p := h.p
	p.goBack()
	deadline := time.Now().Add(3 * time.Second)
	for p.about == nil && time.Now().Before(deadline) {
		h.frame()
		time.Sleep(time.Millisecond)
	}
	if p.about == nil {
		t.Fatal("no bio")
	}
	var urls []string
	for _, r := range p.about.runs {
		if r.URL != "" {
			urls = append(urls, r.URL)
		}
	}
	if want := []string{"t.me/gonews", "https://t.me/gopher_bot", "https://go.dev"}; !slices.Equal(urls, want) {
		t.Fatalf("links %q", urls)
	}
}

// drag presses at from, moves to each of to, and releases at the last.
func (h *sharedHarness) drag(from f32.Point, to ...f32.Point) {
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: from})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: from})
	h.frame()
	for _, at := range to {
		h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at})
		h.frame()
	}
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: to[len(to)-1]})
	for range 10 {
		h.frame()
	}
}

// selectedIndexes are the indexes of the files selected, in order.
func (h *sharedHarness) selectedIndexes() []int {
	var out []int
	for i, m := range h.p.messages {
		if h.p.renderer.selection.selected[m.Key.MessageID] {
			out = append(out, i)
		}
	}
	return out
}

// With nothing selected, a drag selects nothing, for none to be selected
// by chance. Once one was, by its menu, a drag across the thumbnails
// selects the range from the one pressed to the one under the pointer,
// whatever the pointer passed over: a jump of it, which left the
// thumbnails between without an event, leaves none of them out, nor a
// release in the gap between two. Dragged back, the range shrinks; started
// on a selected one, it unselects.
func TestSharedDragSelects(t *testing.T) {
	h := newSharedHarness(t, model.SharedPhotos)
	var opened []model.MessageID
	h.p.renderer.openPhoto = func(m model.Message) { opened = append(opened, m.Key.MessageID) }
	gap := tileAt(6).Add(f32.Pt(76, 0))
	h.drag(tileAt(1), tileAt(1).Add(f32.Pt(12, 0)), gap)
	if got := h.selectedIndexes(); len(got) > 0 || len(opened) > 0 {
		t.Fatalf("with nothing selected: selected %v, opened %v", got, opened)
	}
	h.p.toggleSelected(h.p.messages[0])

	// From the second to the seventh in one move, released in the gap after
	// the seventh.
	h.drag(tileAt(1), tileAt(1).Add(f32.Pt(12, 0)), gap)
	if got := h.selectedIndexes(); !slices.Equal(got, []int{0, 1, 2, 3, 4, 5, 6}) || len(opened) > 0 {
		t.Fatalf("selected %v, opened %v", got, opened)
	}
	// From the tenth to the eighth and back to the ninth: 7, 8, 9 and those.
	h.drag(tileAt(9), tileAt(7), tileAt(8))
	if got := h.selectedIndexes(); !slices.Equal(got, []int{0, 1, 2, 3, 4, 5, 6, 8, 9}) {
		t.Fatalf("drag back: %v", got)
	}
	// From a selected one, unselecting, out of the list below.
	h.drag(tileAt(5), f32.Pt(400, 790))
	if got := h.selectedIndexes(); !slices.Equal(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("unselecting: %v", got)
	}
	if len(opened) > 0 {
		t.Fatalf("opened %v", opened)
	}
}

// The rows of a section select by a drag as the thumbnails do.
func TestSharedDragSelectsRows(t *testing.T) {
	h := newSharedHarness(t, model.SharedLinks)
	p := h.p
	if len(p.places) < 3 {
		t.Fatalf("places %v", p.places)
	}
	// Dialog's origin, (180, 30), and the header over the list.
	origin := f32.Pt(180, 30+float32(p.headerHeight))
	at := func(i int) f32.Point {
		r := p.places[i].rect
		return origin.Add(f32.Pt(float32(r.Min.X+r.Max.X)/2, float32(r.Min.Y+r.Max.Y)/2))
	}
	p.toggleSelected(p.messages[5])
	h.drag(at(0), at(2))
	if got := h.selectedIndexes(); !slices.Equal(got, []int{0, 1, 2, 5}) {
		t.Fatalf("selected %v", got)
	}
}

// A snapshot of a photo selected among the shared ones, whose picture in
// the size of a message's bubble has not loaded yet, waits for it rather
// than drawing it loading.
func TestSharedSnapshotWaitsForPictures(t *testing.T) {
	h := newSharedHarness(t, model.SharedPhotos)
	p := h.p
	p.toggleSelected(p.messages[0])
	d := &p.renderer.shot
	d.open(p.renderer, p.renderer.selectedMessages())
	h.frame()
	if d.rendering || d.image != nil || !d.stale {
		t.Fatalf("drawn at once: rendering %v, image %v", d.rendering, d.image != nil)
	}
	deadline := time.Now().Add(10 * time.Second)
	for d.image == nil && d.err == nil && time.Now().Before(deadline) {
		h.frame()
		time.Sleep(5 * time.Millisecond)
	}
	if d.image == nil || d.err != nil {
		t.Fatalf("no snapshot: %v", d.err)
	}
	if time.Since(d.opened) >= shotMediaWait {
		t.Fatal("drawn only once the wait was over")
	}
}

// A segment crosses a rectangle when any of it is in it: through a corner
// too, but not ending before it or passing by.
func TestSegmentHits(t *testing.T) {
	r := image.Rect(100, 100, 200, 200)
	for _, c := range []struct {
		a, b f32.Point
		hit  bool
	}{
		{f32.Pt(0, 150), f32.Pt(300, 150), true},
		{f32.Pt(190, 95), f32.Pt(205, 110), true},  // a corner's chord
		{f32.Pt(195, 90), f32.Pt(210, 105), false}, // by a corner
		{f32.Pt(0, 0), f32.Pt(99, 99), false},
		{f32.Pt(0, 250), f32.Pt(300, 250), false},
		{f32.Pt(150, 150), f32.Pt(150, 150), true},
		{f32.Pt(250, 0), f32.Pt(250, 300), false},
	} {
		if got := segmentHits(c.a, c.b, r); got != c.hit {
			t.Errorf("%v-%v: %v", c.a, c.b, got)
		}
	}
}

// After the cursor, a drag selects the thumbnails the pointer passed over,
// the ones a jump of it crossed between two events too, and no others.
func TestSharedDragSelectsAfterCursor(t *testing.T) {
	h := newSharedHarness(t, model.SharedPhotos)
	h.p.selectionMode = func() string { return preferences.SharedSelectionCursor }
	h.p.toggleSelected(h.p.messages[12])
	// Along the first row to the third, then in one event across the
	// seventh and the sixth to the tenth.
	h.drag(tileAt(0), tileAt(1), tileAt(2), tileAt(9))
	if got := h.selectedIndexes(); !slices.Equal(got, []int{0, 1, 2, 5, 6, 9, 12}) {
		t.Fatalf("selected %v", got)
	}
	// Back over them from a selected one, unselecting what it crosses.
	h.drag(tileAt(2), tileAt(0))
	if got := h.selectedIndexes(); !slices.Equal(got, []int{5, 6, 9, 12}) {
		t.Fatalf("unselected to %v", got)
	}
}

// The three dots by the close button open the ways a drag selects; the
// one chosen is kept.
func TestSharedSelectionModeMenu(t *testing.T) {
	h := newSharedHarness(t, model.SharedPhotos)
	p := h.p
	mode := ""
	p.selectionMode = func() string { return mode }
	p.setSelectionMode = func(m string) error { mode = m; return nil }
	for range 5 {
		h.frame()
	}
	h.click(pointer.ButtonPrimary, f32.Pt(744, 66))
	if !p.mode.open {
		t.Fatal("the menu did not open")
	}
	b := p.mode.menu.bounds
	h.click(pointer.ButtonPrimary, f32.Pt(float32(b.Min.X+40), float32(b.Min.Y+menuPadding+menuItemHeight+menuItemHeight/2)))
	if mode != preferences.SharedSelectionCursor || p.mode.open || !p.cursorSelection() {
		t.Fatalf("chose %q, open %v", mode, p.mode.open)
	}
}

// What plays is not told in the info: its sections have no player's bar.
func TestSharedSectionsHaveNoAudioBar(t *testing.T) {
	t.Chdir("../../..")
	h := newSharedHarness(t, model.SharedVoice)
	p := h.p
	p.renderer.audio.play = func(src audio.Source) (audioPlayback, error) {
		return &silentPlayback{src: src, playing: true}, nil
	}
	if len(p.messages) == 0 {
		t.Fatal("no voice messages")
	}
	m := p.messages[0]
	p.renderer.audio.toggle(p.renderer, m, -1)
	waitAudio(t, "it did not start", func() bool { return p.renderer.audio.state(m).playing })
	for range 5 {
		h.frame()
	}
	if p.renderer.audioBar.play != nil {
		t.Fatal("the info drew the player's bar")
	}
	p.renderer.audio.stop()
}

// The grid has as many thumbnails a row as chosen, and the slider under
// the ways a drag selects chooses them.
func TestSharedGridColumns(t *testing.T) {
	h := newSharedHarness(t, model.SharedPhotos)
	p := h.p
	columns := 0
	p.columns = func() int { return columns }
	p.setColumns = func(n int) error { columns = n; return nil }
	p.setSelectionMode = func(string) error { return nil }
	perRow := func() int {
		n := 0
		for _, place := range p.places {
			if place.rect.Min.Y == p.places[0].rect.Min.Y {
				n++
			}
		}
		return n
	}
	columns = 6
	for range 5 {
		h.frame()
	}
	if got := perRow(); got != 6 {
		t.Fatalf("%d a row, want 6", got)
	}
	columns = 3
	for range 5 {
		h.frame()
	}
	if got := perRow(); got != 3 {
		t.Fatalf("%d a row, want 3", got)
	}
	// The slider's end, under the ways in the menu.
	h.click(pointer.ButtonPrimary, f32.Pt(744, 66))
	if !p.mode.open {
		t.Fatal("the menu did not open")
	}
	b := p.mode.menu.bounds
	h.click(pointer.ButtonPrimary, f32.Pt(float32(b.Max.X-20), float32(b.Max.Y-menuPadding-26)))
	if columns != 6 {
		t.Fatalf("the slider chose %d", columns)
	}
}
