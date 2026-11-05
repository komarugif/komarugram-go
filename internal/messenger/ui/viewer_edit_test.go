// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/indicator"
	"gio-mw/widget/radio"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/photoedit"
	"komarugram/internal/messenger/sendfiles"
	"komarugram/pkg/program"
	"komarugram/pkg/video"
)

// renderEditor opens the editor on m, as the test of the viewer's picture wants
// it, and waits for its picture.
func renderEditor(t *testing.T, v *mediaViewer, m model.Message, variant string, frame func(), changed chan struct{}) {
	t.Helper()
	v.openEdit(m)
	e := &v.edit
	deadline := time.Now().Add(20 * time.Second)
	for e.open && e.scene == nil && time.Now().Before(deadline) {
		frame()
		select {
		case <-changed:
		case <-time.After(20 * time.Millisecond):
		}
	}
	if e.scene == nil {
		t.Fatal("the editor did not load")
	}
	size := e.scene.Size
	w, h := float64(size.X), float64(size.Y)
	red, white, blue := editPalette[2], editPalette[1], editPalette[7]
	line := func(tool photoedit.Tool, col color.NRGBA, width float64, pts ...photoedit.Point) {
		e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Tool: tool, Color: col, Width: width, Points: pts}})
	}
	drawn := func() {
		line(photoedit.Pen, red, w/90, photoedit.Point{X: w * .1, Y: h * .2}, photoedit.Point{X: w * .2, Y: h * .35}, photoedit.Point{X: w * .35, Y: h * .25}, photoedit.Point{X: w * .45, Y: h * .4})
		line(photoedit.Arrow, white, w/120, photoedit.Point{X: w * .55, Y: h * .15}, photoedit.Point{X: w * .7, Y: h * .2}, photoedit.Point{X: w * .85, Y: h * .3})
		line(photoedit.Marker, editPalette[4], w/70, photoedit.Point{X: w * .1, Y: h * .55}, photoedit.Point{X: w * .5, Y: h * .55})
		line(photoedit.Blur, white, w/60, photoedit.Point{X: w * .55, Y: h * .5}, photoedit.Point{X: w * .9, Y: h * .55})
		line(photoedit.Pen, blue, w/90, photoedit.Point{X: w * .1, Y: h * .7}, photoedit.Point{X: w * .4, Y: h * .7})
		line(photoedit.Eraser, white, w/50, photoedit.Point{X: w * .25, Y: h * .6}, photoedit.Point{X: w * .25, Y: h * .8})
	}
	shapes := func() {
		for i, typ := range []photoedit.ShapeType{photoedit.ShapeCircle, photoedit.ShapeRectangle, photoedit.ShapeStar, photoedit.ShapeBubble, photoedit.ShapeArrow} {
			bh := h * .16
			if typ == photoedit.ShapeStar {
				bh = w * .15 * photoedit.StarAspect
			}
			if typ == photoedit.ShapeArrow {
				bh = h * .06
			}
			e.scene.Add(&photoedit.Element{
				Shape:  &photoedit.Shape{Type: typ, Color: editPalette[2+i], Filled: i%2 == 1, Width: w / 150},
				Center: photoedit.Point{X: w * (.14 + .18*float64(i)), Y: h * .78}, Width: w * .15, Height: bh, Rotation: float64(i) * 3,
			})
		}
	}
	placeText := func(words string, style photoedit.TextStyle, y float64) {
		frame()
		i := e.scene.Add(&photoedit.Element{Text: &photoedit.Text{Color: editPalette[2], Style: style, Size: h * .07, Wrap: 14}, Center: photoedit.Point{X: w / 2, Y: y}, Width: w / 2, Height: h * .1})
		gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(800, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineDark()))
		e.changeText(gtx, i, func(x *photoedit.Text) { x.Text = words })
	}
	settle := func() {
		for range 10 {
			frame()
			time.Sleep(60 * time.Millisecond)
		}
	}
	switch variant {
	case "paint":
		drawn()
		shapes()
	case "crop":
		drawn()
		e.mode = editCrop
		e.scene.Transform.Crop = image.Rect(size.X/6, size.Y/8, size.X*5/6, size.Y*7/8)
		e.scene.Transform.Angle = 90
	case "text":
		placeText("Обычный текст", photoedit.TextPlain, h*.2)
		placeText("В рамке 🙂", photoedit.TextFramed, h*.4)
		placeText("Полупрозрачный", photoedit.TextSemiTransparent, h*.6)
		placeText("Непрозрачный\nв две строки", photoedit.TextOpaque, h*.8)
		e.selected = 1
	case "shapes":
		shapes()
		e.shapesOpen = true
		e.selected = -1
	case "turned":
		drawn()
		e.scene.Transform.Angle = 270
		e.scene.Transform.Flip = true
		e.scene.Transform.Crop = image.Rect(size.X/10, 0, size.X, size.Y*9/10)
	case "opening", "opening-late":
		// Halfway: the picture on its way up, the bar faded, the strip going
		// down, and the rows coming. Late, they show what the picture took.
		e.transition.hold, e.transition.progress, e.transition.alpha, e.transition.tools = true, .5, 0, 1
		if variant == "opening-late" {
			e.transition.progress = .8
		}
	case "trim":
		drawn()
		// The row of a video, over the picture of its frame.
		e.video = true
		e.media.Duration = 95 * time.Second
		e.scene.Trim = photoedit.Trim{From: 12 * time.Second, To: 71 * time.Second}
	case "stickers":
		frame()
		e.stickers = nil
		e.openStickers(v, localization.For("ru"))
	case "choice":
		drawn()
		s := &e.send
		s.radios = radio.NewRadios([]editAction{editReply, editReplace}, editReply, func(a editAction) { s.action = a })
		s.ask(editDialogChoice)
	case "work":
		drawn()
		s := &e.send
		job := &editJob{stop: func() {}, done: make(chan editOutcome, 1), ended: make(chan struct{}), stage: stageEncoding, share: .42}
		s.job, s.bar = job, indicator.Linear()
		s.ask(editDialogWork)
	case "failed":
		drawn()
		s := &e.send
		s.failure = model.ErrEditForbidden
		s.ask(editDialogFailed)
	case "discard":
		drawn()
		e.send.ask(editDialogDiscard)
	default:
		t.Fatalf("no variant %q of the editor", variant)
	}
	settle()
}

// viewerEditHarness drives an editor in a viewer through its real layout, with real
// events.
type viewerEditHarness struct {
	t      *testing.T
	store  *mockstore.Store
	v      *mediaViewer
	router *input.Router
	ops    *op.Ops
	gtx    layout.Context
	size   image.Point
	m      model.Message
	// animate is what the frames tell the viewer of animations.
	animate bool
}

func newViewerEditHarness(t *testing.T, want func(model.Message) bool) *viewerEditHarness {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	var found model.Message
	for _, m := range store.History(2).Messages {
		if want(m) {
			found = m
		}
	}
	if found.Media == nil {
		t.Skip("no such message in the demo")
	}
	h := &viewerEditHarness{t: t, store: store, router: new(input.Router), ops: new(op.Ops), size: image.Pt(1000, 700), m: found}
	h.v = newMediaViewer(store, &imageOps{}, func() {})
	t.Cleanup(h.v.Destroy)
	h.v.share.chats = store.Chats
	h.v.Open(2, found, nil)
	return h
}

func (h *viewerEditHarness) frame() {
	h.t.Helper()
	h.ops.Reset()
	h.gtx = layout.Context{Ops: h.ops, Now: time.Now(), Constraints: layout.Exact(h.size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}, Source: h.router.Source()}
	wdk.InitMaterialThemeInContext(h.gtx, defaults.NewTheme(h.gtx, schemes.SchemeBaselineDark()))
	h.v.images.BeginFrame()
	h.v.Layout(h.gtx, localization.For("ru"), h.animate)
	h.v.images.EndFrame()
	h.router.Frame(h.ops)
}

// load opens the editor and waits for its picture.
func (h *viewerEditHarness) load() *viewerEdit {
	h.t.Helper()
	h.v.openEdit(h.m)
	e := &h.v.edit
	deadline := time.Now().Add(20 * time.Second)
	for e.open && e.scene == nil && time.Now().Before(deadline) {
		h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	if e.scene == nil {
		h.t.Fatal("the editor did not load")
	}
	h.frame()
	h.frame()
	return e
}

func (h *viewerEditHarness) press(at f32.Point) {
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at})
	h.frame()
}

func (h *viewerEditHarness) dragTo(at f32.Point) {
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at})
	h.frame()
}

func (h *viewerEditHarness) lift(at f32.Point) {
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	h.frame()
}

func (h *viewerEditHarness) key(name key.Name, mods key.Modifiers) {
	h.router.Queue(key.Event{Name: name, State: key.Press, Modifiers: mods})
	h.frame()
}

func ownPhoto(m model.Message) bool {
	return m.Kind == model.MessagePhoto && m.Media != nil && m.Outgoing && !m.NoForwards
}

func anyPhoto(m model.Message) bool {
	return m.Kind == model.MessagePhoto && m.Media != nil && !m.NoForwards
}

// Escape closes the editor, not the viewer, and only then the viewer's own keys
// come back.
func TestEditEscapeStaysInEditor(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	h.load()
	h.key(key.NameEscape, 0)
	if h.v.edit.open {
		t.Fatal("Escape left the editor open")
	}
	if !h.v.open {
		t.Fatal("Escape closed the viewer with the editor")
	}
	h.frame()
	h.key(key.NameEscape, 0)
	if h.v.open {
		t.Fatal("the next Escape did not close the viewer")
	}
}

// With something done, Escape asks first, and the viewer stays through all of it.
func TestEditEscapeAsksWhenChanged(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[2], Width: 4, Points: []photoedit.Point{{X: 1, Y: 1}}}})
	h.frame()
	h.key(key.NameEscape, 0)
	if !e.open || e.send.dialog != editDialogDiscard {
		t.Fatalf("open %v, dialog %d", e.open, e.send.dialog)
	}
	if !h.v.open {
		t.Fatal("the viewer closed")
	}
}

// Escape in a dialog closes the dialog; the editor stays.
func TestEditEscapeClosesDialogFirst(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[2], Width: 4, Points: []photoedit.Point{{X: 1, Y: 1}}}})
	h.frame()
	h.key(key.NameEscape, 0)
	if e.send.dialog != editDialogDiscard {
		t.Fatal("no dialog")
	}
	for range 3 {
		h.frame()
	}
	h.key(key.NameEscape, 0)
	for range 40 {
		h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	if !e.open || e.send.busy() {
		t.Fatalf("open %v, dialog %d", e.open, e.send.dialog)
	}
}

// Escape while writing ends the writing, an empty text going with it, and the
// editor stays.
func TestEditEscapeEndsText(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	e := h.load()
	e.newText(h.gtx)
	h.frame()
	if !e.text.editing || len(e.scene.Elements) != 1 {
		t.Fatalf("editing %v, elements %d", e.text.editing, len(e.scene.Elements))
	}
	h.key(key.NameEscape, 0)
	if e.text.editing || !e.open {
		t.Fatalf("editing %v, open %v", e.text.editing, e.open)
	}
	if len(e.scene.Elements) != 0 {
		t.Error("an empty text stayed")
	}
}

// A drag on the row of a video's length moves the handle nearest to it, and
// asks for the frame there.
func TestEditTrimDrag(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	e := h.load()
	e.video, e.media.Duration = true, 10*time.Second
	h.frame()
	y := float32(h.size.Y - h.gtx.Dp(editBarRow) - h.gtx.Dp(editToolRow) - h.gtx.Dp(editTrimRow)/2)
	pad := float32(h.gtx.Dp(90))
	track := float32(h.size.X) - 2*pad
	at := func(share float32) f32.Point { return f32.Pt(pad+track*share, y) }
	h.press(at(.1))
	h.dragTo(at(.2))
	h.lift(at(.2))
	if d := e.scene.Trim.From - 2*time.Second; d < -100*time.Millisecond || d > 100*time.Millisecond {
		t.Errorf("from %v", e.scene.Trim.From)
	}
	if e.scene.Trim.To != 0 {
		t.Errorf("to %v", e.scene.Trim.To)
	}
	h.press(at(.9))
	h.lift(at(.9))
	if d := e.scene.Trim.To - 9*time.Second; d < -100*time.Millisecond || d > 100*time.Millisecond {
		t.Errorf("to %v", e.scene.Trim.To)
	}
	if e.frameDue.IsZero() || e.frameAt != e.scene.Trim.To {
		t.Errorf("no frame asked: due %v at %v", e.frameDue, e.frameAt)
	}
	if !e.scene.Changed() {
		t.Error("a trim is no change")
	}
}

// A press on the panel of stickers does not draw on the picture under it.
func TestEditStickerPanelTakesPresses(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	e := h.load()
	e.openStickers(h.v, localization.For("en"))
	h.frame()
	h.frame()
	at := f32.Pt(float32(h.size.X)-30, float32(h.size.Y)-float32(h.gtx.Dp(editBarRow)+h.gtx.Dp(editToolRow))-150)
	h.press(at)
	h.lift(at)
	if len(e.scene.Elements) != 0 {
		t.Fatal("the press drew under the panel")
	}
	if !e.stickers.shown {
		t.Fatal("the press closed the panel")
	}
}

// waitEdit lays frames out until the editor closes.
func (h *viewerEditHarness) waitEdit() {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for h.v.edit.open && time.Now().Before(deadline) {
		h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	if h.v.edit.open {
		h.t.Fatal("the editor did not close")
	}
}

// Without a right to replace it, what was edited goes at once as a reply to the
// message, in the store's history.
func TestEditFlowReplies(t *testing.T) {
	h := newViewerEditHarness(t, func(m model.Message) bool { return anyPhoto(m) && !m.Outgoing })
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[2], Width: 4, Points: []photoedit.Point{{X: 1, Y: 1}, {X: 50, Y: 50}}}})
	before := len(h.store.History(2).Messages)
	e.finish(h.gtx, h.v, localization.For("en"))
	if e.send.dialog != editDialogWork {
		t.Fatalf("dialog %d: no reason to ask", e.send.dialog)
	}
	h.waitEdit()
	got := h.store.History(2).Messages
	if len(got) != before+1 {
		t.Fatalf("%d messages, were %d", len(got), before)
	}
	last := got[len(got)-1]
	if last.ReplyToMessageID != h.m.Key.MessageID || last.Kind != model.MessagePhoto {
		t.Errorf("sent %+v", last)
	}
	if !h.v.open {
		t.Error("the viewer closed with the editor")
	}
}

// Where the file may be replaced the dialog asks, by radio buttons with the
// reply first; the replacement changes the file of the message.
func TestEditFlowAsksAndReplaces(t *testing.T) {
	h := newViewerEditHarness(t, ownPhoto)
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[2], Width: 4, Points: []photoedit.Point{{X: 1, Y: 1}, {X: 50, Y: 50}}}})
	before := len(h.store.History(2).Messages)
	e.finish(h.gtx, h.v, localization.For("en"))
	if e.send.dialog != editDialogChoice {
		t.Fatalf("dialog %d, want the choice", e.send.dialog)
	}
	if e.send.action != editReply || e.send.radios.GetValue() != editReply {
		t.Fatal("the reply is not the first offered")
	}
	e.send.modal.Hide()
	e.start(h.gtx, h.v, editReplace)
	h.waitEdit()
	got := h.store.History(2).Messages
	if len(got) != before {
		t.Errorf("%d messages, were %d", len(got), before)
	}
	for _, m := range got {
		if m.Key == h.m.Key && (m.Media == nil || m.Media.ID == h.m.Media.ID) {
			t.Errorf("the file stayed: %+v", m.Media)
		}
	}
}

// Done, clicked as the user clicks it, opens the dialog in the same frame, and
// its radios are drawn at once; Continue, clicked too, sends what they say. The
// first frame of the dialog panicked: the radios had not been updated.
func TestEditFlowByClicks(t *testing.T) {
	for _, c := range []struct {
		name   string
		action editAction
	}{{"reply", editReply}, {"replace", editReplace}} {
		t.Run(c.name, func(t *testing.T) {
			h := newViewerEditHarness(t, ownPhoto)
			e := h.load()
			e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[2], Width: 4, Points: []photoedit.Point{{X: 1, Y: 1}, {X: 50, Y: 50}}}})
			before := len(h.store.History(2).Messages)
			h.frame()
			e.done.click.Click()
			h.frame()
			if e.send.dialog != editDialogChoice {
				t.Fatalf("dialog %d, want the choice", e.send.dialog)
			}
			h.frame()
			e.send.radios.SetValue(c.action)
			e.send.action = c.action
			e.send.confirm.click.Click()
			h.frame()
			h.waitEdit()
			got := h.store.History(2).Messages
			if c.action == editReply {
				if len(got) != before+1 || got[len(got)-1].ReplyToMessageID != h.m.Key.MessageID {
					t.Fatalf("%d messages, were %d", len(got), before)
				}
				return
			}
			if len(got) != before {
				t.Fatalf("%d messages, were %d", len(got), before)
			}
			for _, m := range got {
				if m.Key == h.m.Key && m.Media.ID == h.m.Media.ID {
					t.Error("the file stayed")
				}
			}
			// The viewer shows the new one, and what was made until it has come.
			items, _, _, _, _ := h.v.snapshot()
			i := indexOf(items, h.m.Key.MessageID)
			if items[i].Media.ID == h.m.Media.ID {
				t.Error("the viewer has the old file")
			}
			if h.v.replaced.key != h.m.Key {
				t.Error("nothing stands in for the new file")
			}
			h.frame()
			if h.v.drawn != h.m.Key.MessageID {
				t.Errorf("drew %v", h.v.drawn)
			}
		})
	}
}

// Every control of the editor, clicked as the user clicks it, draws in the same
// frame and the ones after: a panel or a dialog opened by a click is first drawn
// in the frame of the click.
func TestEditEveryControlDraws(t *testing.T) {
	h := newViewerEditHarness(t, ownPhoto)
	e := h.load()
	click := func(c *widget.Clickable) {
		t.Helper()
		c.Click()
		h.frame()
		h.frame()
	}
	press := func(s *surface) {
		t.Helper()
		s.click.Click()
		h.frame()
		h.frame()
	}
	// Painting.
	for i := range e.tools {
		click(&e.tools[i])
		if e.tool != photoedit.Tool(i) {
			t.Fatalf("tool %d not taken", i)
		}
	}
	for i := range e.colors {
		click(&e.colors[i])
	}
	click(&e.sticker)
	if e.stickers == nil || !e.stickers.shown {
		t.Fatal("no panel of stickers")
	}
	click(&e.sticker)
	click(&e.shapesButton)
	for i := range e.shapes {
		click(&e.shapes[i])
	}
	click(&e.fill)
	click(&e.shapesButton)
	// A figure and what acts on it.
	e.armed, e.armedShape = true, photoedit.ShapeBubble
	from := e.toScreen(photoedit.Point{X: float64(e.scene.Size.X) / 4, Y: float64(e.scene.Size.Y) / 4})
	to := e.toScreen(photoedit.Point{X: float64(e.scene.Size.X) / 2, Y: float64(e.scene.Size.Y) / 2})
	h.press(from)
	h.dragTo(to)
	h.lift(to)
	if e.selected < 0 {
		t.Fatal("the figure is not selected")
	}
	click(&e.fill)
	click(&e.flipItem)
	click(&e.repeat)
	click(&e.colors[3])
	click(&e.remove)
	click(&e.undo)
	click(&e.redo)
	// A text and what acts on it.
	click(&e.addText)
	if !e.text.editing {
		t.Fatal("not writing")
	}
	e.text.editor.SetText("слова")
	e.changeText(h.gtx, e.text.at, func(x *photoedit.Text) { x.Text = "слова" })
	h.frame()
	press(&e.styleButton)
	press(&e.fontButton)
	click(&e.alignButton)
	press(&e.text.ok)
	if e.text.editing {
		t.Fatal("still writing")
	}
	// Cropping.
	click(&e.modeCrop)
	for i := range e.aspects {
		press(&e.aspects[i])
	}
	click(&e.rotate)
	click(&e.flip)
	click(&e.modePaint)
	// Each dialog, and out of it.
	press(&e.cancelEdit)
	if e.send.dialog != editDialogDiscard {
		t.Fatalf("Cancel: dialog %d", e.send.dialog)
	}
	press(&e.send.cancel)
	for range 40 {
		h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	if e.send.busy() {
		t.Fatalf("the dialog stayed: %d", e.send.dialog)
	}
	press(&e.done)
	if e.send.dialog != editDialogChoice {
		t.Fatalf("Done: dialog %d", e.send.dialog)
	}
	press(&e.send.cancel)
	for range 40 {
		h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	if !e.open || e.send.busy() {
		t.Fatalf("open %v, dialog %d", e.open, e.send.dialog)
	}
}

// The share of a stage never goes back, as FFmpeg's count does at its end; the
// next stage starts its own.
func TestEditProgressOnlyGrows(t *testing.T) {
	j := &editJob{share: -1}
	j.report(stagePreparing, -1)
	if _, share := j.state(); share != -1 {
		t.Errorf("before any share: %v", share)
	}
	j.report(stageEncoding, .5)
	j.report(stageEncoding, .99)
	j.report(stageEncoding, .96)
	if stage, share := j.state(); stage != stageEncoding || share != .99 {
		t.Errorf("went back: %v %v", stage, share)
	}
	j.report(stageSending, -1)
	if stage, share := j.state(); stage != stageSending || share != -1 {
		t.Errorf("the next stage: %v %v", stage, share)
	}
	j.report(stageSending, .1)
	if _, share := j.state(); share != .1 {
		t.Errorf("its share: %v", share)
	}
}

// What stands in for the new file is the view as it comes out, cut and turned.
func TestEditResultPicture(t *testing.T) {
	size := image.Pt(100, 60)
	e := &viewerEdit{scene: photoedit.New(size)}
	e.scene.Transform.Crop = image.Rect(10, 10, 90, 50)
	e.scene.Transform.Angle = 90
	stage := image.Rect(0, 0, 80, 80)
	e.picture, e.pictureScale = image.NewRGBA(image.Rectangle{Max: size}), 1
	e.sync(stage)
	img, _ := e.preview.Render(nil, true, nil)
	e.shown = img
	got := e.resultPicture()
	if got == nil {
		t.Fatal("none")
	}
	if b := got.Bounds().Size(); b.Y <= b.X {
		t.Errorf("of %v, not turned", b)
	}
	// With the whole picture in view, while cropping, it is cut all the same.
	e.mode = editCrop
	e.sync(stage)
	img, _ = e.preview.Render(nil, true, nil)
	e.shown = img
	got = e.resultPicture()
	if got == nil {
		t.Fatal("none while cropping")
	}
	want := float64(e.scene.Transform.Crop.Dy()) / float64(e.scene.Transform.Crop.Dx())
	if b := got.Bounds().Size(); float64(b.X)/float64(b.Y) < want*.8 || float64(b.X)/float64(b.Y) > want*1.2 {
		t.Errorf("of %v, want proportions %v", b, want)
	}
}

// Cancel and Done take their own height in a row that hands down its own: the
// plate stays around the text, and the row centers it.
func TestEditBarButtonHeight(t *testing.T) {
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 200)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineDark()))
	var s surface
	dims := editBarButton(gtx, &s, "Done", token.NewMatColorFromHexRGB(0xffffff))
	if dims.Size.Y >= 200 || dims.Size.X >= 600 {
		t.Errorf("the button takes %v of the row", dims.Size)
	}
}

// The steps of the way follow one another: the bar goes first, the strip with it,
// the rows come last, and at each end all is at rest. On the way back the same
// numbers run backward, which is what puts them in the reverse order.
func TestEditTransitionPhases(t *testing.T) {
	at := func(p float32) *viewerEdit {
		e := &viewerEdit{open: true}
		e.transition.progress = p
		return e
	}
	if e := at(0); e.chromeAlpha() != 1 || e.stripShift() != 0 || e.controlsAlpha() != 0 {
		t.Errorf("at the viewer: chrome %v, strip %v, rows %v", e.chromeAlpha(), e.stripShift(), e.controlsAlpha())
	}
	if e := at(1); e.chromeAlpha() != 0 || e.stripShift() != 1 || e.controlsAlpha() != 1 {
		t.Errorf("at the editor: chrome %v, strip %v, rows %v", e.chromeAlpha(), e.stripShift(), e.controlsAlpha())
	}
	if e := at(.2); e.chromeAlpha() >= 1 || e.stripShift() <= 0 || e.controlsAlpha() != 0 {
		t.Errorf("early: chrome %v, strip %v, rows %v: the rows come after the rest goes", e.chromeAlpha(), e.stripShift(), e.controlsAlpha())
	}
	if e := at(.8); e.chromeAlpha() != 0 || e.controlsAlpha() <= 0 {
		t.Errorf("late: chrome %v, rows %v", e.chromeAlpha(), e.controlsAlpha())
	}
	// With no editor all is the viewer's.
	if e := (&viewerEdit{}); e.chromeAlpha() != 1 || e.stripShift() != 0 {
		t.Error("no editor moves the viewer")
	}
}

// The picture goes straight from where the viewer has it to where the editor
// does, and is exactly at each at the ends.
func TestEditPictureMoves(t *testing.T) {
	e := &viewerEdit{open: true, stage: image.Rect(20, 20, 980, 600)}
	from, size := image.Pt(100, 100), image.Pt(800, 400)
	native := image.Pt(1600, 800)
	_, target := editFit(native, e.stage)
	e.transition.progress = 0
	if o, s := e.pictureAt(from, size, native); o != from || s != size {
		t.Errorf("at the start: %v %v", o, s)
	}
	e.transition.progress = 1
	if o, s := e.pictureAt(from, size, native); o != target.Min || s != target.Size() {
		t.Errorf("at the end: %v %v, want %v", o, s, target)
	}
	e.transition.progress = .5
	o, s := e.pictureAt(from, size, native)
	if o.Y >= from.Y && target.Min.Y < from.Y {
		t.Errorf("halfway at %v from %v to %v", o, from, target.Min)
	}
	if s.X < min(size.X, target.Dx()) || s.X > max(size.X, target.Dx()) {
		t.Errorf("halfway of width %d, from %d to %d", s.X, size.X, target.Dx())
	}
}

// Until the editor's picture has come, and the way is over, the viewer's stays
// shown, and all of it: that gap was the blink. Once the editor's covers it, it
// is not drawn.
func TestEditPictureHandsOver(t *testing.T) {
	e := &viewerEdit{open: true}
	e.transition.progress = .5
	if got := e.pictureAlpha(); got != 1 {
		t.Errorf("on the way: %v", got)
	}
	e.transition.progress = 1
	if got := e.pictureAlpha(); got != 1 {
		t.Errorf("waiting for the picture: %v", got)
	}
	e.scene = photoedit.New(image.Pt(100, 100))
	e.transition.alpha = .5
	if got := e.pictureAlpha(); got != 1 {
		t.Errorf("under the editor's that fades in: %v", got)
	}
	e.transition.alpha = 1
	if got := e.pictureAlpha(); got != 0 {
		t.Errorf("covered: %v", got)
	}
	// Cropped, it is not the same picture: they cross.
	e.scene.Transform.Crop = image.Rect(10, 10, 90, 90)
	e.transition.alpha = .25
	if got := e.pictureAlpha(); got != .75 {
		t.Errorf("cropped: %v", got)
	}
}

// With animations on the editor goes in and comes out over frames, the viewer
// staying open, its own controls taking nothing meanwhile.
func TestEditTransitionRuns(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	h.animate = true
	h.v.openEdit(h.m)
	e := &h.v.edit
	h.frame()
	if !e.open || e.transition.progress >= 1 {
		t.Fatalf("no way in: open %v, at %v", e.open, e.transition.progress)
	}
	h.v.close.Click()
	h.frame()
	if !h.v.open {
		t.Fatal("the viewer's button closed it under the editor")
	}
	deadline := time.Now().Add(20 * time.Second)
	for (e.transition.progress < 1 || e.transition.alpha < 1) && time.Now().Before(deadline) {
		h.frame()
		if e.transition.progress < 1 && e.transition.alpha > 0 {
			t.Fatal("the editor's picture shows while the viewer's is on its way")
		}
		if e.scene != nil && e.transition.tools <= 0 && e.transition.alpha > 0 {
			t.Fatal("the tools wait for the picture's fade")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !e.settled() {
		t.Fatalf("not settled: at %v, alpha %v", e.transition.progress, e.transition.alpha)
	}
	h.key(key.NameEscape, 0)
	if !e.open || !e.transition.leaving {
		t.Fatalf("no way out: open %v, leaving %v", e.open, e.transition.leaving)
	}
	if e.settled() {
		t.Error("it takes input on the way out")
	}
	for e.open && time.Now().Before(deadline) {
		h.frame()
		time.Sleep(5 * time.Millisecond)
	}
	if e.open {
		t.Fatal("the way out did not end")
	}
	if !h.v.open {
		t.Fatal("the viewer closed with the editor")
	}
}

// A slow frame does not skip the way: its time counts only up to a limit, or the
// frame that decodes all would jump to the end.
func TestEditSlowFrameKeepsTheWay(t *testing.T) {
	e := &viewerEdit{open: true}
	now := time.Now()
	gtx := layout.Context{Ops: new(op.Ops), Now: now, Values: map[string]any{}}
	e.stepTransition(gtx, true)
	gtx.Now = now.Add(5 * time.Second)
	e.stepTransition(gtx, true)
	if e.transition.progress >= .5 {
		t.Errorf("at %v after one slow frame", e.transition.progress)
	}
}

// Writing a text does not move the picture, whose row comes over it.
func TestEditTextDoesNotMovePicture(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	e := h.load()
	stage, view := e.stage, e.view
	e.newText(h.gtx)
	h.frame()
	if e.stage != stage || e.view.origin != view.origin || e.view.scale != view.scale {
		t.Errorf("stage %v from %v, at %v from %v", e.stage, stage, e.view.origin, view.origin)
	}
}

// clipSource is a store with a video, whose reply it keeps a copy of.
type clipSource struct {
	*mockstore.Store
	clip []byte
	out  string
	sent model.OutgoingMessage
}

func (c *clipSource) Media(ctx context.Context, m model.Message) ([]byte, error) {
	return c.clip, ctx.Err()
}
func (c *clipSource) MediaStream(ctx context.Context, m model.Message) (io.ReaderAt, int64, func(), error) {
	return bytes.NewReader(c.clip), int64(len(c.clip)), func() {}, ctx.Err()
}
func (c *clipSource) QueueMessage(_ context.Context, chat int64, msg model.OutgoingMessage) error {
	c.sent = msg
	data, err := os.ReadFile(msg.Files.Paths[0])
	if err != nil {
		return err
	}
	return os.WriteFile(c.out, data, 0o644)
}
func (c *clipSource) RetryOutgoing(model.MessageKey)  {}
func (c *clipSource) CancelOutgoing(model.MessageKey) {}

// A phone's turned video goes all the way, as the editor does it: opened, drawn
// on, cropped, trimmed, encoded and sent as a reply, of the size asked.
func TestEditVideoFlow(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	dir := t.TempDir()
	straight := filepath.Join(dir, "straight.mp4")
	turned := filepath.Join(dir, "turned.mp4")
	for _, args := range [][]string{
		{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=320x240:d=2:r=25", "-f", "lavfi", "-t", "2", "-i", "sine", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", straight},
		{"-v", "error", "-y", "-display_rotation", "90", "-i", straight, "-c", "copy", turned},
	} {
		if out, err := program.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	clip, err := os.ReadFile(turned)
	if err != nil {
		t.Fatal(err)
	}
	store := mockstore.New(time.Now(), 0)
	source := &clipSource{Store: store, clip: clip, out: filepath.Join(dir, "out.mp4")}
	m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: 2, MessageID: 900}, Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "clip", MIMEType: "video/mp4", Size: int64(len(clip))}, Date: time.Now()}
	h := &viewerEditHarness{t: t, store: store, router: new(input.Router), ops: new(op.Ops), size: image.Pt(1000, 700), m: m}
	h.v = newMediaViewer(source, &imageOps{}, func() {})
	t.Cleanup(h.v.Destroy)
	h.v.ffmpeg = func() string { return ffmpeg }
	h.v.openList(2, m, mediaList{items: []model.Message{m}, ended: [2]bool{true, true}, gallery: galleryPhotos})
	if !h.v.canEdit(m) {
		t.Fatal("the video may not be edited")
	}
	e := h.load()
	if e.scene.Size != image.Pt(240, 320) {
		t.Fatalf("a turned video edits at %v", e.scene.Size)
	}
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[2], Width: 6, Points: []photoedit.Point{{X: 10, Y: 10}, {X: 200, Y: 300}}}})
	e.scene.Transform.Crop = image.Rect(21, 41, 221, 301)
	e.scene.Trim = photoedit.Trim{From: 500 * time.Millisecond, To: 1500 * time.Millisecond}
	h.frame()
	e.finish(h.gtx, h.v, localization.For("en"))
	h.waitEdit()
	if source.sent.ReplyTo != m.Key.MessageID {
		t.Fatalf("sent %+v", source.sent)
	}
	out, err := video.ProbeMedia(context.Background(), source.out, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if out.EditSize() != image.Pt(200, 260) || !out.Sound {
		t.Errorf("came out %+v", out)
	}
	if d := out.Duration - time.Second; d < -150*time.Millisecond || d > 150*time.Millisecond {
		t.Errorf("lasts %v", out.Duration)
	}
	if _, err := os.Stat(source.sent.Files.Paths[0]); err == nil {
		t.Error("the temporary file stayed")
	}
}

// A GIF goes the way of a video in the editor, and comes out a GIF: without sound,
// sent as one.
func TestEditGIFFlow(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	dir := t.TempDir()
	clipPath := filepath.Join(dir, "clip.mp4")
	if out, err := program.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=320x240:d=1:r=25", "-f", "lavfi", "-t", "1", "-i", "sine", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", clipPath).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	clip, err := os.ReadFile(clipPath)
	if err != nil {
		t.Fatal(err)
	}
	store := mockstore.New(time.Now(), 0)
	source := &clipSource{Store: store, clip: clip, out: filepath.Join(dir, "out.mp4")}
	m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: 2, MessageID: 901}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "clip", MIMEType: "video/mp4", Size: int64(len(clip)), Duration: time.Second}, Date: time.Now()}
	h := &viewerEditHarness{t: t, store: store, router: new(input.Router), ops: new(op.Ops), size: image.Pt(1000, 700), m: m}
	h.v = newMediaViewer(source, &imageOps{}, func() {})
	t.Cleanup(h.v.Destroy)
	h.v.ffmpeg = func() string { return ffmpeg }
	h.v.openList(2, m, mediaList{items: []model.Message{m}, ended: [2]bool{true, true}, gallery: galleryGIFs})
	if !h.v.canEdit(m) {
		t.Fatal("the GIF may not be edited")
	}
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[2], Width: 6, Points: []photoedit.Point{{X: 10, Y: 10}, {X: 200, Y: 200}}}})
	h.frame()
	e.finish(h.gtx, h.v, localization.For("en"))
	h.waitEdit()
	if source.sent.Files == nil || !source.sent.Files.IsAnimated(0) || source.sent.ReplyTo != m.Key.MessageID {
		t.Fatalf("sent %+v", source.sent)
	}
	out, err := video.ProbeMedia(context.Background(), source.out, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if out.Sound {
		t.Error("a GIF with sound")
	}
	if out.EditSize() != image.Pt(320, 240) {
		t.Errorf("came out %v", out.EditSize())
	}
}

// A GIF of a known length has the plate of a video, without what is about sound;
// its bar moves it, a press on it pauses it, and the keys do as on a video.
func TestGIFBar(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var m model.Message
	for _, o := range store.History(2).Messages {
		if o.Kind == model.MessageGIF && o.Media != nil && o.Media.ID == "demo/gif" {
			m = o
		}
	}
	if m.Media == nil || m.Media.Duration <= 0 {
		t.Fatal("the demo has no GIF with a length")
	}
	h := &viewerEditHarness{t: t, store: store, router: new(input.Router), ops: new(op.Ops), size: image.Pt(1000, 700), m: m}
	h.v = newMediaViewer(store, &imageOps{}, func() {})
	t.Cleanup(h.v.Destroy)
	h.v.OpenGIF(2, m, nil)
	c := &h.v.controls
	for deadline := time.Now().Add(5 * time.Second); c.width <= 0 && time.Now().Before(deadline); {
		h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	s := h.v.playState(m)
	if !s.active || !s.playing || s.duration != m.Media.Duration {
		t.Fatalf("state %+v", s)
	}
	if c.width <= 0 {
		t.Fatal("no bar was laid out")
	}
	// The plate lies over the bottom of the stage, the bar ending at its right.
	stageBottom := h.size.Y - h.gtx.Dp(viewerStrip)
	y := float32(stageBottom - h.gtx.Dp(12) - h.gtx.Dp(videoPlate)/2)
	plateWidth := min(h.size.X-2*h.gtx.Dp(viewerSide)-2*h.gtx.Dp(12), h.gtx.Dp(720))
	right := float32((h.size.X+plateWidth)/2 - h.gtx.Dp(4) - h.gtx.Dp(8))
	at := f32.Pt(right-float32(c.width)/2, y)
	h.press(at)
	if !c.seeking {
		t.Fatal("a press on the bar does not move it")
	}
	h.lift(at)
	want := m.Media.Duration / 2
	if got := h.v.full.GIFPosition(m); got < want-m.Media.Duration/10 || got > want+m.Media.Duration/10 {
		t.Errorf("at %v, want about %v", got, want)
	}
	h.v.togglePlay(m)
	if s := h.v.playState(m); s.playing {
		t.Error("not paused")
	}
	h.v.togglePlay(m)
	before := h.v.full.GIFPosition(m)
	h.v.videoKey(h.gtx, m, "viewer.seek_back")
	if got := h.v.full.GIFPosition(m); got >= before && before > 0 {
		t.Errorf("the key moved it from %v to %v", before, got)
	}
	// Paused, it stays so for this viewer only.
	h.v.togglePlay(m)
	h.v.Close()
	if h.v.full.GIFPaused(m) {
		t.Error("a pause outlived the viewer")
	}
}

// A GIF without a length has no bar, a video none until it plays.
func TestPlayStateOf(t *testing.T) {
	v := newMediaViewer(mockstore.New(time.Now(), 0), &imageOps{}, func() {})
	defer v.Destroy()
	if s := v.playState(model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "x"}}); s.active {
		t.Error("a GIF of no length")
	}
	if s := v.playState(model.Message{Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "x", Duration: time.Second}}); s.active {
		t.Error("a video that does not play")
	}
	if s := v.playState(model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "x", Duration: time.Second}}); s.active {
		t.Error("a photo")
	}
}

// boxEditHarness is a box's viewer on a file on the disk.
func boxEditHarness(t *testing.T, name string, write func(path string)) (*viewerEditHarness, *messageComposer, *boxFile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	write(path)
	info, err := sendfiles.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &boxFile{File: info, ready: true}
	store := mockstore.New(time.Now(), 0)
	c := newMessageComposer(store, func() {})
	t.Cleanup(c.cancel)
	media, items, current := boxGallery(store, 2, []*boxFile{f}, f)
	media.ffmpeg, media.replaced = func() string { return "" }, c.replaceBoxPath
	h := &viewerEditHarness{t: t, store: store, router: new(input.Router), ops: new(op.Ops), size: image.Pt(1000, 700), m: current}
	h.v = newMediaViewer(media, &imageOps{}, func() {})
	t.Cleanup(h.v.Destroy)
	h.v.editSink, h.v.stickerStore = media.edit, store
	h.v.openList(2, current, mediaList{items: items, ended: [2]bool{true, true}, gallery: galleryBox})
	return h, c, f, path
}

func writeTestPicture(t *testing.T, path string, encode func(*os.File, image.Image) error) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	img := image.NewRGBA(image.Rect(0, 0, 120, 80))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i-3], img.Pix[i] = 200, 255
	}
	if err := encode(out, img); err != nil {
		t.Fatal(err)
	}
}

// Done in the box's viewer asks nothing and sends nothing: the box holds what was
// made, under the name the file had, the file the user chose staying as it was,
// and the viewer shows the new one.
func TestBoxEditReplacesTheFile(t *testing.T) {
	h, c, f, path := boxEditHarness(t, "shot.png", func(path string) {
		writeTestPicture(t, path, func(w *os.File, img image.Image) error { return png.Encode(w, img) })
	})
	// The box's source has nothing to reply with: only what it takes back offers it.
	if !h.v.canEditNow(h.m) {
		t.Fatal("the box's viewer offers no editor")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := len(h.store.History(2).Messages)
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[1], Width: 8, Points: []photoedit.Point{{X: 0, Y: 40}, {X: 120, Y: 40}}}})
	e.scene.Transform.Crop = image.Rect(0, 0, 100, 60)
	h.frame()
	e.done.click.Click()
	h.frame()
	if e.send.dialog == editDialogChoice {
		t.Fatal("the box's viewer asks to reply")
	}
	h.waitEdit()
	if got := len(h.store.History(2).Messages); got != before {
		t.Fatalf("%d messages, were %d: it sent something", got, before)
	}
	if f.Path == path {
		t.Fatal("the box still holds the file as it was")
	}
	if filepath.Base(f.Path) != "shot.png" || f.Name != "shot.png" {
		t.Errorf("named %q, %q", f.Path, f.Name)
	}
	if f.Width != 100 || f.Height != 60 || f.Kind != sendfiles.KindPhoto || !f.ready {
		t.Errorf("the file %+v", f.File)
	}
	if now, err := os.ReadFile(path); err != nil || !bytes.Equal(now, original) {
		t.Errorf("the file chosen changed: %v", err)
	}
	if !c.pasted[f.Path] {
		t.Error("nothing removes the result")
	}
	items, _, _, _, _ := h.v.snapshot()
	if items[0].Media.ID == h.m.Media.ID || items[0].Media.Width != 100 {
		t.Errorf("the viewer shows %+v", items[0].Media)
	}
	if data, err := h.v.source.Media(context.Background(), items[0]); err != nil || len(data) == 0 {
		t.Errorf("the viewer reads the new one: %v", err)
	}
	// Edited again, the result before it goes; closed, this one too.
	first := f.Path
	c.replaceBoxPath(first, filepath.Join(t.TempDir(), "next.png"))
	if _, err := os.Stat(first); err == nil {
		t.Error("the result replaced stayed")
	}
	c.forgetPasted(nil)
	if len(c.pasted) != 0 {
		t.Errorf("left %v", c.pasted)
	}
}

// A photo that came as a JPEG stays one.
func TestBoxEditKeepsJPEG(t *testing.T) {
	h, _, f, _ := boxEditHarness(t, "photo.jpg", func(path string) {
		writeTestPicture(t, path, func(w *os.File, img image.Image) error { return jpeg.Encode(w, img, nil) })
	})
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[1], Width: 8, Points: []photoedit.Point{{X: 0, Y: 40}, {X: 120, Y: 40}}}})
	h.frame()
	e.finish(h.gtx, h.v, localization.For("en"))
	h.waitEdit()
	if f.MIME != "image/jpeg" || filepath.Base(f.Path) != "photo.jpg" {
		t.Errorf("%q %q", f.MIME, f.Path)
	}
}

// Left without Done, it changes nothing.
func TestBoxEditCancelKeepsTheFile(t *testing.T) {
	h, c, f, path := boxEditHarness(t, "photo.png", func(path string) {
		writeTestPicture(t, path, func(w *os.File, img image.Image) error { return png.Encode(w, img) })
	})
	h.load()
	h.key(key.NameEscape, 0)
	if f.Path != path || len(c.pasted) != 0 {
		t.Errorf("path %q, kept %v", f.Path, c.pasted)
	}
	if !h.v.open {
		t.Error("the viewer closed with the editor")
	}
}

// The panel of stickers has the account's, the box's source having none.
func TestBoxEditStickers(t *testing.T) {
	h, _, _, _ := boxEditHarness(t, "photo.png", func(path string) {
		writeTestPicture(t, path, func(w *os.File, img image.Image) error { return png.Encode(w, img) })
	})
	e := h.load()
	e.sticker.Click()
	h.frame()
	if e.stickers == nil || !e.stickers.shown || e.stickers.grid == nil {
		t.Fatal("no panel of its own")
	}
	if e.stickers.picker.source == nil {
		t.Fatal("the panel has nowhere to ask")
	}
	for deadline := time.Now().Add(5 * time.Second); len(e.stickers.picker.page.Packs) == 0 && time.Now().Before(deadline); {
		h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	if len(e.stickers.picker.page.Packs) == 0 {
		t.Error("no stickers came")
	}
}

// A .gif edited stays a GIF of the box: an MP4 that goes as one, its neighbours
// staying what they are.
func TestBoxEditGIF(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	h, _, f, _ := boxEditHarness(t, "clip.gif", func(path string) {
		if out, err := program.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=160x120:d=0.5:r=10", path).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	})
	if f.Kind != sendfiles.KindAnimation {
		t.Fatalf("kind %v", f.Kind)
	}
	h.v.ffmpeg = func() string { return ffmpeg }
	h.v.canEditNow(h.m)
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[1], Width: 4, Points: []photoedit.Point{{X: 0, Y: 40}, {X: 120, Y: 40}}}})
	h.frame()
	e.finish(h.gtx, h.v, localization.For("en"))
	h.waitEdit()
	if f.Kind != sendfiles.KindAnimation || f.MIME != "video/mp4" || filepath.Base(f.Path) != "clip.mp4" {
		t.Fatalf("the file %+v", f.File)
	}
	if f.thumb == nil || f.long <= 0 {
		t.Errorf("thumb %v, long %v", f.thumb != nil, f.long)
	}
	items, _, _, _, _ := h.v.snapshot()
	if items[0].Kind != model.MessageGIF {
		t.Errorf("the viewer shows kind %v", items[0].Kind)
	}
	other := &boxFile{File: sendfiles.File{Kind: sendfiles.KindVideo, MIME: "video/mp4"}}
	gif := &boxFile{File: sendfiles.File{Kind: sendfiles.KindAnimation, MIME: "image/gif"}}
	if got := boxAnimated([]*boxFile{other, f, gif}); len(got) != 1 || got[0] != 1 {
		t.Errorf("places %v", got)
	}
}

// A video edited shows as what it became, in the frames after the one that took
// the result, which still drew the item it had: what the viewer keeps of a video's
// thumbnail goes by the message, and that frame put the old one back.
func TestBoxEditVideoShowsTheResult(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	h, _, f, _ := boxEditHarness(t, "clip.mp4", func(path string) {
		if out, err := program.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=160x120:d=0.5:r=10", "-c:v", "libx264", "-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	})
	h.v.ffmpeg = func() string { return ffmpeg }
	picture, err := sendfiles.VideoThumbnail(context.Background(), ffmpeg, f.Path, boxThumbSide)
	if err != nil {
		t.Fatal(err)
	}
	f.thumb = picture
	media := h.v.source.(*boxMedia)
	media.ffmpeg = func() string { return ffmpeg }
	id := h.m.Key.MessageID
	h.m.Kind, h.m.Media = media.add("box/0", f.File, picture, image.Pt(160, 120), 500*time.Millisecond)
	h.v.openList(2, h.m, mediaList{items: []model.Message{h.m}, ended: [2]bool{true, true}, gallery: galleryBox})
	h.frame()
	if target, ok := h.v.viewerTarget(h.m); !ok || target.Media.ID != h.m.Media.Thumbnail.ID {
		t.Fatalf("before: %+v", target.Media)
	}
	e := h.load()
	e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: editPalette[1], Width: 4, Points: []photoedit.Point{{X: 0, Y: 40}, {X: 120, Y: 40}}}})
	e.scene.Transform.Crop = image.Rect(0, 0, 100, 80)
	h.frame()
	e.finish(h.gtx, h.v, localization.For("en"))
	h.waitEdit()
	h.frame()
	items, _, _, _, _ := h.v.snapshot()
	now := items[indexOf(items, id)]
	if now.Media.ID == h.m.Media.ID || now.Media.Thumbnail == nil {
		t.Fatalf("the item: %+v", now.Media)
	}
	target, ok := h.v.viewerTarget(now)
	if !ok || target.Media.ID != now.Media.Thumbnail.ID {
		t.Errorf("the viewer shows %q, the file's picture is %q", target.Media.ID, now.Media.Thumbnail.ID)
	}
	if target.Media.Width != now.Media.Width {
		t.Errorf("of width %d, the file's %d", target.Media.Width, now.Media.Width)
	}
	// So does the strip, and it plays the new file.
	if st := h.v.thumb(target, 56); st.Media.ID != now.Media.Thumbnail.ID {
		t.Errorf("the strip shows %q", st.Media.ID)
	}
	if kept, ok := media.file(now.Media.ID); !ok || kept.path != f.Path {
		t.Errorf("it would play %q, the file is %q", kept.path, f.Path)
	}
}

// What is kept of a thumbnail is of the file it was made for.
func TestViewerKeepsThumbnailsOfTheFile(t *testing.T) {
	v := newMediaViewer(mockstore.New(time.Now(), 0), &imageOps{}, func() {})
	defer v.Destroy()
	key := model.MessageKey{ChatID: 2, MessageID: 5}
	of := func(id string) model.Message {
		return model.Message{Key: key, Kind: model.MessageVideo, Media: &model.MessageMedia{ID: id, Width: 100, Height: 50, Thumbnail: &model.MessageMedia{ID: id + "/thumb"}}}
	}
	if t1, _ := v.thumbnailTarget(of("a")); t1.Media.ID != "a/thumb" {
		t.Fatalf("first: %q", t1.Media.ID)
	}
	if t2, _ := v.thumbnailTarget(of("b")); t2.Media.ID != "b/thumb" {
		t.Errorf("after the file changed: %q", t2.Media.ID)
	}
	a, b := of("a"), of("b")
	if m := v.thumb(a, 56); m.Media.ID != "a" {
		t.Fatalf("thumb: %q", m.Media.ID)
	}
	if m := v.thumb(b, 56); m.Media.ID != "b" {
		t.Errorf("thumb after the file changed: %q", m.Media.ID)
	}
}

// The box sends as the settings choose.
func TestBoxSendsSilentVideoAsChosen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	writeTestPicture(t, path, func(w *os.File, img image.Image) error { return png.Encode(w, img) })
	info, err := sendfiles.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, chosen := range []bool{false, true} {
		c := newMessageComposer(mockstore.New(time.Now(), 0), func() {})
		defer c.cancel()
		c.chat = 2
		c.silentAsVideo = func() bool { return chosen }
		c.files.files = []*boxFile{{File: info, ready: true}}
		c.files.modal.Open()
		c.sendFiles(localization.For("en"))
		pending := c.draft(2).pending
		if pending == nil || pending.Files == nil || pending.Files.SilentAsVideo != chosen {
			t.Errorf("chose %v, went %+v", chosen, pending)
		}
	}
}

// A result nobody is left to take is removed.
func TestBoxEditDiscards(t *testing.T) {
	h, _, _, _ := boxEditHarness(t, "photo.png", func(path string) {
		writeTestPicture(t, path, func(w *os.File, img image.Image) error { return png.Encode(w, img) })
	})
	dir := t.TempDir()
	made := filepath.Join(dir, "photo.png")
	writeTestPicture(t, made, func(w *os.File, img image.Image) error { return png.Encode(w, img) })
	edited, err := h.v.editSink(context.Background(), h.m, made)
	if err != nil {
		t.Fatal(err)
	}
	media := h.v.source.(*boxMedia)
	kept, ok := media.file(edited.message.Media.ID)
	if !ok {
		t.Fatal("not kept")
	}
	if _, err := os.Stat(kept.path); err != nil {
		t.Fatal(err)
	}
	edited.discard()
	if _, err := os.Stat(kept.path); err == nil {
		t.Error("it stayed")
	}
}

// A drag on the picture draws a stroke at the place of the picture it went over,
// which undoes and comes back by the keys.
func TestEditStrokeAndUndo(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	e := h.load()
	if e.mode != editPaint {
		t.Fatal("not painting")
	}
	from := e.toScreen(photoedit.Point{X: float64(e.scene.Size.X) / 4, Y: float64(e.scene.Size.Y) / 4})
	to := e.toScreen(photoedit.Point{X: float64(e.scene.Size.X) / 2, Y: float64(e.scene.Size.Y) / 2})
	h.press(from)
	h.dragTo(to)
	h.lift(to)
	if len(e.scene.Elements) != 1 || e.scene.Elements[0].Stroke == nil {
		t.Fatalf("elements: %v", e.scene.Elements)
	}
	s := e.scene.Elements[0].Stroke
	last := s.Points[len(s.Points)-1]
	if d := last.Sub(photoedit.Point{X: float64(e.scene.Size.X) / 2, Y: float64(e.scene.Size.Y) / 2}).Len(); d > 3/e.view.scale {
		t.Errorf("the stroke ends %v off", d)
	}
	h.key("Z", key.ModShortcut)
	if len(e.scene.Elements) != 0 {
		t.Fatal("not undone")
	}
	h.key("Z", key.ModShortcut|key.ModShift)
	if len(e.scene.Elements) != 1 {
		t.Fatal("not redone")
	}
}

// The photo saved is what the editor draws, with what was done to it, turned and
// cut, at the size of the picture.
func TestEditSavedPhotoIsTheScene(t *testing.T) {
	size := image.Pt(200, 120)
	picture := image.NewRGBA(image.Rectangle{Max: size})
	for i := 0; i < len(picture.Pix); i += 4 {
		picture.Pix[i], picture.Pix[i+1], picture.Pix[i+2], picture.Pix[i+3] = 128, 128, 128, 255
	}
	s := photoedit.New(size)
	s.Add(&photoedit.Element{Stroke: &photoedit.Stroke{Color: color.NRGBA{R: 255, A: 255}, Width: 10, Points: []photoedit.Point{{X: 0, Y: 30}, {X: 200, Y: 30}}}})
	s.Transform.Crop = image.Rect(20, 0, 180, 100)
	s.Transform.Angle = 90
	path := filepath.Join(t.TempDir(), "out.jpg")
	if err := savePhotoResult(s, picture, nil, path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Size(); got != image.Pt(100, 160) {
		t.Fatalf("size %v", got)
	}
	// The line went from y 30 of the picture to x 100-30 of the turned one.
	r, _, b, _ := img.At(70, 80).RGBA()
	if r>>8 < 200 || b>>8 > 80 {
		t.Errorf("no line where it should be: %d %d", r>>8, b>>8)
	}
	r, _, _, _ = img.At(30, 80).RGBA()
	if r>>8 > 160 {
		t.Errorf("a line where there is none: %d", r>>8)
	}
}

// fakeSender records what comes of a result.
type fakeSender struct {
	*mockstore.Store
	queued   []model.OutgoingMessage
	queuedIn []int64
	data     []byte
	replaced []model.Message
	fail     error
}

func (f *fakeSender) QueueMessage(_ context.Context, chat int64, msg model.OutgoingMessage) error {
	f.queued, f.queuedIn = append(f.queued, msg), append(f.queuedIn, chat)
	f.data, _ = os.ReadFile(msg.Files.Paths[0])
	return nil
}
func (f *fakeSender) RetryOutgoing(model.MessageKey)  {}
func (f *fakeSender) CancelOutgoing(model.MessageKey) {}
func (f *fakeSender) ReplaceMedia(_ context.Context, m model.Message, path, _ string, progress func(int64, int64)) (model.Message, error) {
	if f.fail != nil {
		return model.Message{}, f.fail
	}
	f.replaced = append(f.replaced, m)
	f.data, _ = os.ReadFile(path)
	return m, nil
}

// A reply goes to the chat shown, to the message edited, as a photo, with no
// file left behind.
func TestEditReplyQueues(t *testing.T) {
	fake := &fakeSender{Store: mockstore.New(time.Now(), 0)}
	m := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 7}, Kind: model.MessagePhoto}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jpg")
	if err := os.WriteFile(path, []byte("the file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := deliverEdited(context.Background(), fake, m, 42, path, "ff", false, editReply, func(editStage, float64) {}); err != nil {
		t.Fatal(err)
	}
	if fake.queued[0].Files.SilentAsVideo {
		t.Error("a video without sound goes as a video by default")
	}
	if _, err := deliverEdited(context.Background(), fake, m, 42, path, "ff", true, editReply, func(editStage, float64) {}); err != nil {
		t.Fatal(err)
	}
	if !fake.queued[1].Files.SilentAsVideo {
		t.Error("the choice did not reach the message")
	}
	fake.queued, fake.queuedIn = fake.queued[:1], fake.queuedIn[:1]
	if len(fake.queued) != 1 || fake.queuedIn[0] != 42 {
		t.Fatalf("queued %+v in %v", fake.queued, fake.queuedIn)
	}
	q := fake.queued[0]
	if q.ReplyTo != 7 || q.RandomID == 0 || q.FFmpeg != "ff" || q.Files == nil || q.Files.Documents || !q.Files.HighQuality {
		t.Errorf("message %+v", q)
	}
	if string(fake.data) != "the file" {
		t.Errorf("file %q", fake.data)
	}
}

func TestEditReplaceReports(t *testing.T) {
	fake := &fakeSender{Store: mockstore.New(time.Now(), 0)}
	m := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 7}, Kind: model.MessagePhoto}
	path := filepath.Join(t.TempDir(), "x.jpg")
	if err := os.WriteFile(path, []byte("the file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := deliverEdited(context.Background(), fake, m, 2, path, "", false, editReplace, func(editStage, float64) {}); err != nil {
		t.Fatal(err)
	}
	if len(fake.replaced) != 1 || fake.replaced[0].Key.MessageID != 7 {
		t.Fatalf("replaced %+v", fake.replaced)
	}
	fake.fail = model.ErrEditForbidden
	if _, err := deliverEdited(context.Background(), fake, m, 2, path, "", false, editReplace, func(editStage, float64) {}); !errors.Is(err, model.ErrEditForbidden) {
		t.Fatalf("err %v", err)
	}
}

// A replacement that fails keeps its file for another try, and a reply; closing
// lets go of it.
func TestEditFailedReplaceKeepsResult(t *testing.T) {
	h := newViewerEditHarness(t, ownPhoto)
	e := h.load()
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept")
	if err := os.Mkdir(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(kept, "photo.jpg")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.frame()
	h.v.editOutcome(h.gtx, editOutcome{dir: kept, path: path, err: model.ErrEditForbidden, action: editReplace}, localization.For("en"))
	if e.send.dialog != editDialogFailed || e.send.path != path {
		t.Fatalf("dialog %d, path %q", e.send.dialog, e.send.path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the file is gone")
	}
	h.v.closeEdit()
	if _, err := os.Stat(path); err == nil {
		t.Error("the file stayed with the editor closed")
	}
}

// Growing a text keeps its lines: the width they wrap at grows with it, in the
// picture the view and the file draw alike.
func TestEditTextKeepsLinesAsItGrows(t *testing.T) {
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(400, 400)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	text := &photoedit.Text{Text: "several words that do not fit in one line", Color: color.NRGBA{A: 255}, Size: 20, Wrap: 8}
	small := recordText(gtx, new(op.Ops), text, 20, textWrapAt(text, 20))
	large := recordText(gtx, new(op.Ops), text, 80, textWrapAt(text, 80))
	ratio := func(p image.Point) float64 { return float64(p.X) / float64(p.Y) }
	if a, b := ratio(small), ratio(large); a/b < .9 || a/b > 1.1 {
		t.Errorf("at 20 px %v, at 80 px %v: other lines", small, large)
	}
}

// The button is there for what may be edited, and for nothing else.
func TestCanEdit(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	v := newMediaViewer(store, &imageOps{}, func() {})
	defer v.Destroy()
	v.ffmpeg = func() string { return "" }
	v.chat = 2
	photo := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 5}, Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "x"}, Date: time.Now()}
	if !v.canEditNow(photo) {
		t.Error("a photo")
	}
	protected := photo
	protected.NoForwards = true
	if v.canEditNow(protected) {
		t.Error("protected content")
	}
	profile := photo
	profile.Key.MessageID = model.ProfilePhotoID(0)
	if v.canEditNow(profile) {
		t.Error("a profile's photo")
	}
	sticker := photo
	sticker.Kind = model.MessageSticker
	if v.canEditNow(sticker) {
		t.Error("a sticker")
	}
	v.gallery = galleryStories
	if v.canEditNow(photo) {
		t.Error("a story")
	}
	v.gallery = galleryBox
	if v.canEditNow(photo) {
		t.Error("a file of a box that takes nothing back")
	}
	v.editSink = func(context.Context, model.Message, string) (editedFile, error) { return editedFile{}, nil }
	if !v.canEditNow(photo) {
		t.Error("a file of the box")
	}
	if v.canEditNow(protected) {
		t.Error("protected content in the box")
	}
}

// What a replacement allows: own message, in time, a photo or video.
func TestCanReplaceMedia(t *testing.T) {
	now := time.Now()
	m := model.Message{Key: model.MessageKey{MessageID: 5}, Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "x"}, Outgoing: true, Date: now}
	limit := 48 * time.Hour
	if !model.CanReplaceMedia(m, model.KindUser, now, limit) {
		t.Error("own photo")
	}
	old := m
	old.Date = now.Add(-49 * time.Hour)
	if model.CanReplaceMedia(old, model.KindUser, now, limit) {
		t.Error("past the limit")
	}
	in := m
	in.Outgoing = false
	if model.CanReplaceMedia(in, model.KindUser, now, limit) {
		t.Error("another's")
	}
	if !model.CanReplaceMedia(in, model.KindSaved, now, limit) {
		t.Error("in Saved Messages")
	}
	round := m
	round.Kind = model.MessageRound
	if model.CanReplaceMedia(round, model.KindUser, now, limit) {
		t.Error("a round video")
	}
	gif := m
	gif.Kind = model.MessageGIF
	if !model.CanReplaceMedia(gif, model.KindUser, now, limit) {
		t.Error("a GIF")
	}
	hidden := m
	hidden.MediaSpoiler = true
	if model.CanReplaceMedia(hidden, model.KindUser, now, limit) {
		t.Error("one that may destruct itself")
	}
}

// A panic in the editor's layout leaves the viewer closed and the next frames
// fine, and lets go of what it held.
func TestEditRecoversFromPanic(t *testing.T) {
	h := newViewerEditHarness(t, anyPhoto)
	e := h.load()
	e.picture = nil
	e.preview.Release()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("no panic with no picture")
			}
		}()
		h.frame()
	}()
	if !h.v.recoverFrame() {
		t.Fatal("the frame was not recovered")
	}
	if h.v.open || h.v.edit.open {
		t.Fatal("the viewer stayed")
	}
	h.frame()
	h.frame()
}

// A text's picture drawn by the GPU has its colors back as they were, and a
// transparent background.
func TestEditTextPicture(t *testing.T) {
	win, err := headless.NewWindow(8, 8)
	if err != nil {
		t.Skip("no GPU")
	}
	win.Release()
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(400, 400)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	text := &photoedit.Text{Text: "WW", Color: color.NRGBA{B: 255, A: 255}, Style: photoedit.TextSemiTransparent, Size: 64}
	ops := new(op.Ops)
	size := recordText(gtx, ops, text, 64, 800)
	if size.X <= 0 || size.Y <= 0 {
		t.Fatalf("size %v", size)
	}
	img, err := drawText(ops, size)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.NRGBAAt(0, 0); got.A != 0 {
		t.Errorf("the corner is %v", got)
	}
	plate, ink := 0, 0
	for i := 0; i < len(img.Pix); i += 4 {
		switch c := img.Pix[i : i+4]; {
		case c[3] == 0x99 && c[0] > 252 && c[1] > 252 && c[2] > 252:
			plate++
		case c[3] == 255 && c[2] > 250 && c[0] < 3:
			ink++
		}
	}
	if plate == 0 {
		t.Error("no plate of white at 0x99, its color lost")
	}
	if ink == 0 {
		t.Error("no ink of the text's color")
	}
}
