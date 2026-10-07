// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

type interactionHarness struct {
	router  input.Router
	page    *chatPage
	now     time.Time
	size    image.Point
	row     *messageRow
	animate bool
}

func newInteractionHarness(t *testing.T, runs []model.TextRun) *interactionHarness {
	t.Helper()
	h := &interactionHarness{page: newChatPage(benchmarkHistory{}, func() {}), now: time.Unix(1000, 0), size: image.Pt(240, 500), row: &messageRow{runs: runs}, animate: true}
	t.Cleanup(h.page.Close)
	h.frame()
	return h
}
func (h *interactionHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Constraints{Max: h.size}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	h.page.keyboardEvents(gtx)
	h.page.richText(gtx, h.row, localization.For("en"), h.animate)
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(16 * time.Millisecond)
}
func (h *interactionHarness) pointer(kind pointer.Kind, pos f32.Point) {
	if kind == pointer.Drag {
		kind = pointer.Move
	}
	h.router.Queue(pointer.Event{Time: time.Duration(h.now.UnixNano()), Kind: kind, Source: pointer.Mouse, Position: pos, Buttons: pointer.ButtonPrimary})
	h.frame()
}
func (h *interactionHarness) shortcut(name key.Name) {
	h.router.Queue(key.Event{Name: name, Modifiers: key.ModShortcut, State: key.Press})
	h.frame()
}
func TestTextSelectionAcrossFormattingAndWrapCopiesUnicode(t *testing.T) {
	runs := []model.TextRun{{Text: "Привет 👋 "}, {Text: "bold текст ", Bold: true}, {Text: "и ещё несколько слов на следующей строке."}}
	h := newInteractionHarness(t, runs)
	fragments := h.row.text.fragments
	first := fragments[0].Clusters[0].Bounds
	last := fragments[len(fragments)-1].Bounds
	h.pointer(pointer.Press, f32.Pt(float32(first.Min.X), float32(first.Min.Y+first.Dy()/2)))
	h.pointer(pointer.Drag, f32.Pt(float32(last.Max.X), float32(last.Min.Y+last.Dy()/2)))
	h.pointer(pointer.Release, f32.Pt(float32(last.Max.X), float32(last.Min.Y+last.Dy()/2)))
	want := ""
	for _, r := range runs {
		want += r.Text
	}
	if got := h.row.selectedText(); got != want {
		t.Fatalf("selection = %q, want %q (%d:%d)", got, want, h.row.text.anchor, h.row.text.caret)
	}
	h.shortcut("C")
	_, data, ok := h.router.WriteClipboard()
	if !ok || string(data) != want {
		t.Fatalf("clipboard = %q, ok %v", data, ok)
	}
	if len(h.page.selection.selected) != 0 {
		t.Fatal("text drag selected messages")
	}
}
func TestSpoilerClickUsesWrappedFragmentOriginAndReducedMotion(t *testing.T) {
	for _, animate := range []bool{true, false} {
		h := newInteractionHarness(t, []model.TextRun{{Text: strings.Repeat("секрет ", 18), Spoiler: true}})
		h.animate = animate
		fragments := h.row.text.fragments
		f := fragments[len(fragments)-1]
		pos := f32.Pt(float32(f.Bounds.Min.X+f.Bounds.Dx()/2), float32(f.Bounds.Min.Y+f.Bounds.Dy()/2))
		h.pointer(pointer.Press, pos)
		h.pointer(pointer.Release, pos)
		if h.row.text.reveal.center != pos {
			t.Fatalf("wrong ripple origin %v, want %v", h.row.text.reveal.center, pos)
		}
		if h.row.revealed == animate {
			t.Fatal("wrong animation state")
		}
		h.now = h.now.Add(spoilerMaxDuration)
		h.frame()
		if !h.row.revealed {
			t.Fatal("spoiler never completed")
		}
	}
}
func TestDraggingLinkAndSpoilerDoesNotActivate(t *testing.T) {
	h := newInteractionHarness(t, []model.TextRun{{Text: "секретная ссылка", Spoiler: true, URL: "https://example.com"}})
	h.pointer(pointer.Press, f32.Pt(1, 10))
	h.pointer(pointer.Drag, f32.Pt(70, 10))
	h.pointer(pointer.Release, f32.Pt(70, 10))
	if !h.row.text.reveal.started.IsZero() || h.row.revealed || h.page.link != "" {
		t.Fatal("drag activated hidden link or spoiler")
	}
	h.shortcut("A")
	h.shortcut("C")
	_, data, _ := h.router.WriteClipboard()
	if strings.Contains(string(data), "секрет") {
		t.Fatal("unrevealed text leaked to clipboard")
	}
	if string(data) != "[•••]" {
		t.Fatalf("clipboard = %q", data)
	}
}
func TestSpoilerRadiusCoversAllCorners(t *testing.T) {
	size := image.Pt(380, 160)
	for _, center := range []f32.Point{f32.Pt(0, 0), f32.Pt(190, 80), f32.Pt(379, 159)} {
		now := time.Unix(1000, 0)
		reveal := spoilerReveal{started: now, center: center}
		if reveal.radius(now, size) != 0 {
			t.Fatal("ripple did not start at zero")
		}
		radius := reveal.radius(now.Add(reveal.duration(size)), size)
		for _, corner := range []image.Point{{0, 0}, {380, 0}, {0, 160}, {380, 160}} {
			if float64(radius)+.001 < math.Hypot(float64(corner.X)-float64(center.X), float64(corner.Y)-float64(center.Y)) {
				t.Fatal("unrevealed corner")
			}
		}
	}
}
func TestMessageRangeReversalDeselectAndDeletion(t *testing.T) {
	p := chatPage{rows: map[model.MessageID]*messageRow{}}
	for _, id := range []model.MessageID{10, 20, 30, 40, 50} {
		p.messages = append(p.messages, model.Message{Key: model.MessageKey{MessageID: id}})
	}
	p.selection = messageSelection{before: map[model.MessageID]bool{50: true}, anchor: 20, selecting: true}
	p.rangeSelection(40)
	if !reflect.DeepEqual(p.selection.selected, map[model.MessageID]bool{20: true, 30: true, 40: true, 50: true}) {
		t.Fatal(p.selection.selected)
	}
	p.rangeSelection(10)
	if !reflect.DeepEqual(p.selection.selected, map[model.MessageID]bool{10: true, 20: true, 50: true}) {
		t.Fatal("reverse drag left rows selected", p.selection.selected)
	}
	p.selection.before = p.selection.selected
	p.selection.anchor = 20
	p.selection.selecting = false
	p.rangeSelection(10)
	if !reflect.DeepEqual(p.selection.selected, map[model.MessageID]bool{50: true}) {
		t.Fatal(p.selection.selected)
	}
	p.pruneSelection(map[model.MessageID]bool{10: true})
	if p.selectionCount() != 0 {
		t.Fatal("deleted message still selected")
	}
}

func TestRTLSelectionUsesVisualGlyphPositions(t *testing.T) {
	text := "مرحبا بالعالم"
	h := newInteractionHarness(t, []model.TextRun{{Text: text}})
	f := h.row.text.fragments[0]
	y := float32(f.Bounds.Min.Y + f.Bounds.Dy()/2)
	h.pointer(pointer.Press, f32.Pt(float32(f.Bounds.Max.X-1), y))
	h.pointer(pointer.Drag, f32.Pt(0, y))
	h.pointer(pointer.Release, f32.Pt(0, y))
	if got := h.row.selectedText(); got != text {
		t.Fatalf("RTL selection %q want %q", got, text)
	}
}

func TestGutterDragRoutesToSelectionAndChatSwitchClears(t *testing.T) {
	now := time.Unix(1000, 0)
	var messages []model.Message
	for i := 1; i <= 4; i++ {
		messages = append(messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}, Date: now, Text: "Select this message", Outgoing: i%2 == 0, ContentRevision: 1})
	}
	p := newChatPage(benchmarkHistory{h: model.History{Messages: messages, Revision: 1}}, func() {})
	defer p.Close()
	var router input.Router
	render := func(chat int64) {
		gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: now, Constraints: layout.Exact(image.Pt(400, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		p.Layout(gtx, model.Chat{ID: chat}, localization.For("en"), true)
		router.Frame(gtx.Ops)
		now = now.Add(16 * time.Millisecond)
	}
	render(1)
	y := func(index int) float32 {
		return float32(int(p.heights.Prefix(index)-p.heights.Prefix(p.list.Position.First)) - p.list.Position.Offset + p.rows[messages[index].Key.MessageID].bodyTop + 10)
	}
	event := func(kind pointer.Kind, ypos float32) {
		router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(2, ypos), Buttons: pointer.ButtonPrimary})
		render(1)
	}
	event(pointer.Press, y(0))
	event(pointer.Move, y(2))
	event(pointer.Release, y(2))
	if p.selectionCount() != 3 {
		t.Fatalf("gutter selection = %v; position %+v", p.selection.selected, p.list.Position)
	}
	router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	render(1)
	if p.selectionCount() != 0 {
		t.Fatal("Escape did not clear selection")
	}
	event(pointer.Press, y(1))
	event(pointer.Release, y(1))
	if p.selectionCount() != 1 {
		t.Fatal("click did not toggle message")
	}
	render(2)
	if p.selectionCount() != 0 || p.activeText != nil {
		t.Fatal("selection leaked into another chat")
	}
}

func TestGutterDragAutoscrollExtendsRange(t *testing.T) {
	now := time.Unix(1000, 0)
	var messages []model.Message
	for i := 1; i <= 30; i++ {
		messages = append(messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}, Date: now, Text: "A message to select", ContentRevision: 1})
	}
	p := newChatPage(benchmarkHistory{h: model.History{Messages: messages, Revision: 1}}, func() {})
	defer p.Close()
	var router input.Router
	render := func() {
		gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: now, Constraints: layout.Exact(image.Pt(400, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		p.Layout(gtx, model.Chat{ID: 1}, localization.For("en"), true)
		router.Frame(gtx.Ops)
		now = now.Add(16 * time.Millisecond)
	}
	render()
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 0, 0, true
	render()
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: f32.Pt(2, 110), Buttons: pointer.ButtonPrimary})
	render()
	router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(2, 560), Buttons: pointer.ButtonPrimary})
	render()
	selected := p.selectionCount()
	for i := 0; i < 80; i++ {
		render()
	}
	if p.list.Position.First == 0 || p.selectionCount() <= selected {
		t.Fatalf("autoscroll failed: first=%d selected=%d before=%d", p.list.Position.First, p.selectionCount(), selected)
	}
	router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(2, 560)})
	render()
	if p.selection.dragging {
		t.Fatal("drag did not finish")
	}
}

func TestSelectionActionsFollowRights(t *testing.T) {
	p := chatPage{messages: []model.Message{{Key: model.MessageKey{MessageID: 1}, Text: "keep me"}}}
	p.selection.selected = map[model.MessageID]bool{1: true}
	// Without a store that forwards or deletes, only the snapshot is offered.
	if r := p.selectionRights(); r.Forward || r.Delete || !r.Save {
		t.Fatalf("rights without a store: %+v", r)
	}
	p.actions.snapshot.click.Click()
	p.actions.forward.click.Click()
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Unix(1000, 0), Constraints: layout.Exact(image.Pt(340, 56)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	p.selectionHeader(gtx, localization.For("en"))
	if !p.shot.modal.Shown() || p.forwarding.modal.Shown() || p.selectionCount() != 1 {
		t.Fatal("the header acted beyond the rights")
	}
	p.messages[0].NoForwards = true
	if r := p.selectionRights(); r.Save {
		t.Fatal("a protected message can be saved as a snapshot")
	}
}

func TestDoubleAndTripleClickSelection(t *testing.T) {
	h := newInteractionHarness(t, []model.TextRun{{Text: "Привет "}, {Text: "мир", Bold: true}, {Text: "! Hello again"}})
	pos := f32.Pt(15, 10)
	click := func() { h.pointer(pointer.Press, pos); h.pointer(pointer.Release, pos) }
	click()
	click()
	if got := h.row.selectedText(); got != "Привет" {
		t.Fatalf("double click selected %q", got)
	}
	click()
	if got := h.row.selectedText(); got != "Привет мир! Hello again" {
		t.Fatalf("triple click selected %q", got)
	}
	if h.page.link != "" {
		t.Fatal("multi-click opened link")
	}
}
func TestCyrillicShortcutsAndShiftSelection(t *testing.T) {
	h := newInteractionHarness(t, []model.TextRun{{Text: "Привет, світ!"}})
	h.pointer(pointer.Press, f32.Pt(0, 10))
	h.pointer(pointer.Release, f32.Pt(0, 10))
	h.router.Queue(key.Event{Name: key.NameRightArrow, Modifiers: key.ModShift, State: key.Press})
	h.frame()
	if got := h.row.selectedText(); got != "П" {
		t.Fatalf("Shift+Right = %q", got)
	}
	h.shortcut("Ф")
	h.shortcut("С")
	_, data, ok := h.router.WriteClipboard()
	if !ok || string(data) != "Привет, світ!" {
		t.Fatalf("Cyrillic copy = %q", data)
	}
}
func TestSelectionBackgroundMergedAcrossStyles(t *testing.T) {
	h := newInteractionHarness(t, []model.TextRun{{Text: "One "}, {Text: "bold ", Bold: true}, {Text: "word", Italic: true}})
	h.row.text.anchor, h.row.text.caret = 0, 13
	regions := h.row.text.selectionRegions()
	if len(regions) != 1 {
		t.Fatalf("selection fragmented into %v", regions)
	}
	if regions[0].Dx() < 50 {
		t.Fatal("selection does not cover line")
	}
}
func TestBatchedDragDoesNotActivateSpoiler(t *testing.T) {
	h := newInteractionHarness(t, []model.TextRun{{Text: "Секретный текст", Spoiler: true}})
	stamp := time.Duration(h.now.UnixNano())
	h.router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: f32.Pt(1, 10), Buttons: pointer.ButtonPrimary, Time: stamp},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(60, 10), Buttons: pointer.ButtonPrimary, Time: stamp + time.Millisecond},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(60, 10), Time: stamp + 2*time.Millisecond},
	)
	h.frame()
	h.frame() // Focus/grab commands may defer pending events to the next Gio frame.
	if !h.row.text.reveal.started.IsZero() {
		t.Fatal("batched drag opened spoiler")
	}
	if h.row.text.anchor == h.row.text.caret {
		t.Fatal("batched drag lost selection")
	}
}

// Over what a click acts on, a link, an entity's action and a spoiler not
// revealed, the cursor is a hand, as over a button; over the rest of the
// text, and a spoiler revealed, it is the text's.
func TestEntityCursors(t *testing.T) {
	runs := []model.TextRun{{Text: "plain "}, {Text: "link", URL: "https://example.com"}, {Text: " "}, {Text: "#tag", Action: "hashtag", Value: "#tag"}, {Text: " "}, {Text: "secret", Spoiler: true}, {Text: " end"}}
	h := newInteractionHarness(t, runs)
	h.animate = false
	cursorOver := func(i int) pointer.Cursor {
		t.Helper()
		for _, f := range h.row.text.fragments {
			if f.Index == i {
				c := f.Bounds.Min.Add(f.Bounds.Size().Div(2))
				h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(c.X), float32(c.Y))})
				h.frame()
				return h.router.Cursor()
			}
		}
		t.Fatalf("run %d is not drawn", i)
		return 0
	}
	for i, want := range []pointer.Cursor{pointer.CursorText, pointer.CursorPointer, pointer.CursorText, pointer.CursorPointer, pointer.CursorText, pointer.CursorPointer, pointer.CursorText} {
		if got := cursorOver(i); got != want {
			t.Errorf("over %q: %v, not %v", runs[i].Text, got, want)
		}
	}
	h.row.revealed = true
	h.frame()
	if got := cursorOver(5); got != pointer.CursorText {
		t.Errorf("over a spoiler revealed: %v", got)
	}
}
