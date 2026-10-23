// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"testing"
	"time"

	"gio-mw/widget/scroll"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"komarugram/internal/messenger/model"
)

// The point of the photo under the pointer stays under it on every frame of
// the animation, not only at its end, unless that would open a gap between
// the photo and the edge of the view: then the photo rests against the edge.
func TestZoomKeepsThePointUnderThePointer(t *testing.T) {
	native, view := image.Pt(1600, 1200), image.Pt(800, 460)
	fit := fitScale(native, image.Pt(656, 460))
	var z viewerZoom
	at := f32.Pt(150, -80)
	z.zoomBy(zoomStep*zoomStep*zoomStep, at, fit)
	point := at.Sub(z.offset).Div(z.scale)
	now := time.Unix(0, 0)
	frames := 0
	check := func() {
		t.Helper()
		d := at.Sub(z.offset).Div(z.scale).Sub(point)
		for _, a := range []struct{ drift, offset, size, view float32 }{
			{d.X, z.offset.X, float32(native.X) * z.scale, float32(view.X)},
			{d.Y, z.offset.Y, float32(native.Y) * z.scale, float32(view.Y)},
		} {
			edge := max(0, (a.size-a.view)/2)
			if math.Abs(float64(a.drift)) > .01 && math.Abs(math.Abs(float64(a.offset))-float64(edge)) > .01 {
				t.Fatalf("frame %d: the point under the pointer drifted by %v with the photo off the edge", frames, d)
			}
		}
	}
	for z.step(now, true, fit, native, view) {
		check()
		now = now.Add(time.Second / 60)
		frames++
	}
	if d := at.Sub(z.offset).Div(z.scale).Sub(point); math.Hypot(float64(d.X), float64(d.Y)) > .01 {
		t.Fatalf("settled with the point under the pointer %v away", d)
	}
	if want := fit * zoomStep * zoomStep * zoomStep; math.Abs(float64(z.scale-want)) > 1e-4 {
		t.Fatalf("settled at %v, want %v", z.scale, want)
	}
	// Smooth, but not sluggish: about a quarter of a second at 60 Hz.
	if frames < 5 || frames > 30 {
		t.Fatalf("zoom took %d frames", frames)
	}
	// Back to fitted: the zoom switches itself off and follows the window.
	z.unzoom(fit)
	for z.step(now, true, fit, native, view) {
		now = now.Add(time.Second / 60)
	}
	if z.active() {
		t.Fatalf("still zoomed after unzoom: %+v", z)
	}
	// Without animations the target is reached at once.
	z.zoomBy(2, f32.Point{}, fit)
	if z.step(now, false, fit, native, view) || z.scale != z.target {
		t.Fatal("zoom animated with animations off")
	}
}

func TestZoomOffsetKeepsPhotoOverView(t *testing.T) {
	native, view := image.Pt(1000, 1000), image.Pt(800, 400)
	// 2000×2000 on screen: at most 600 px off centre across, 800 down.
	if got := clampOffset(f32.Pt(5000, -5000), 2, native, view); got != f32.Pt(600, -800) {
		t.Fatal(got)
	}
	// Narrower than the view across: centred that way, free the other.
	if got := clampOffset(f32.Pt(100, 100), .5, native, view); got != f32.Pt(0, 50) {
		t.Fatal(got)
	}
}

// scroll turns the wheel by notches at pos, at once.
func (h *viewerHarness) scroll(pos f32.Point, notches float32, mods key.Modifiers) {
	h.router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: pos, Scroll: f32.Pt(0, notches*scroll.NotchPixels), Modifiers: mods, Wheel: true})
	h.frame()
}

// swipe scrolls a touchpad by dy pixels at pos.
func (h *viewerHarness) swipe(pos f32.Point, dy float32) {
	h.router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: pos, Scroll: f32.Pt(0, dy)})
	h.frame()
}

// fixedScrollScales makes a notch scroll.NotchPixels, and a touchpad's
// pixels pixels, whatever the platform the test runs on.
func fixedScrollScales(t *testing.T) {
	wheel, touchpad := scroll.WheelScale, scroll.TouchpadScale
	scroll.WheelScale, scroll.TouchpadScale = 1, 1
	t.Cleanup(func() { scroll.WheelScale, scroll.TouchpadScale = wheel, touchpad })
}

func (h *viewerHarness) drag(from, to f32.Point, steps int) {
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: from, Buttons: pointer.ButtonPrimary})
	h.frame()
	for i := 1; i <= steps; i++ {
		p := from.Add(to.Sub(from).Mul(float32(i) / float32(steps)))
		h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p, Buttons: pointer.ButtonPrimary})
		h.frame()
	}
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: to})
	h.frame()
}

func (h *viewerHarness) settle() {
	for i := 0; i < 120 && (h.viewer.zoom.active() && h.viewer.zoom.scale != h.viewer.zoom.target); i++ {
		h.frame()
	}
}

// Layout at 800×600: the view spans y 56…516, the side zones x 0…72 and
// 728…800, the ✕ is centred at 768, 28 and the window button at 720, 28.
func TestMediaViewerWheelZoomAndSides(t *testing.T) {
	fixedScrollScales(t)
	h := newViewerHarness(t)
	s := h.store
	h.viewer.Open(1, s.photos[14], s.photos)
	h.frame()

	// Each notch of the wheel switches at once, however fast they come, as
	// in Telegram Desktop.
	h.scroll(f32.Pt(400, 300), 1, 0)
	if h.viewer.current != 160 {
		t.Fatalf("wheel down showed %d", h.viewer.current)
	}
	h.scroll(f32.Pt(400, 300), 1, 0)
	h.scroll(f32.Pt(400, 300), 1, 0)
	if h.viewer.current != 180 {
		t.Fatalf("two more notches at once showed %d, want 180", h.viewer.current)
	}
	// Windows joins the notches of a fast wheel into one event.
	h.scroll(f32.Pt(400, 300), -2, 0)
	if h.viewer.current != 160 {
		t.Fatalf("two joined notches up showed %d, want 160", h.viewer.current)
	}
	// X11 sends a notch as two halves; KDE Plasma as one and a half notches,
	// still one photo each.
	h.scroll(f32.Pt(400, 300), .5, 0)
	h.scroll(f32.Pt(400, 300), .5, 0)
	h.scroll(f32.Pt(400, 300), 1.5, 0)
	h.scroll(f32.Pt(400, 300), 1.5, 0)
	if h.viewer.current != 190 {
		t.Fatalf("two halves and two notches of KDE Plasma showed %d, want 190", h.viewer.current)
	}
	// A touchpad switches each notch's distance, what is left kept.
	for range 7 {
		h.swipe(f32.Pt(400, 300), -30)
	}
	if h.viewer.current != 170 {
		t.Fatalf("a swipe of 210 px up showed %d, want 170", h.viewer.current)
	}
	h.swipe(f32.Pt(400, 300), -90)
	if h.viewer.current != 160 {
		t.Fatalf("90 px more showed %d, want 160", h.viewer.current)
	}
	h.scroll(f32.Pt(400, 300), -1, 0)

	// Anywhere in the side zones switches, not only on the arrow.
	h.click(20, 70)
	if h.viewer.current != 140 {
		t.Fatalf("click at the top of the left zone showed %d", h.viewer.current)
	}
	h.click(790, 500)
	if h.viewer.current != 150 {
		t.Fatalf("click at the bottom of the right zone showed %d", h.viewer.current)
	}

	// Ctrl with the wheel zooms around the pointer and decodes the original.
	fit := fitScale(image.Pt(1600, 1200), image.Pt(656, 460))
	for range 3 {
		h.scroll(f32.Pt(500, 200), -1, key.ModCtrl)
	}
	h.settle()
	if want := fit * zoomStep * zoomStep * zoomStep; math.Abs(float64(h.viewer.zoom.scale-want)) > 1e-4 || h.viewer.current != 150 {
		t.Fatalf("zoomed to %v on %d, want %v on 150", h.viewer.zoom.scale, h.viewer.current, want)
	}
	h.until("the original at its own size", func() bool {
		f := h.viewer.full.StatusFit(s.photos[14], false, image.Pt(1600, 1200), false).Frame
		return f != nil && f.Bounds().Size() == image.Pt(1600, 1200)
	})

	// Enlarged, a drag moves the photo and clicks nothing, and the wheel
	// moves it instead of switching.
	before := h.viewer.zoom.offset
	h.drag(f32.Pt(400, 300), f32.Pt(460, 260), 6)
	if moved := h.viewer.zoom.offset.Sub(before); moved != f32.Pt(60, -40) {
		t.Fatalf("drag moved the photo by %v", moved)
	}
	if !h.viewer.open || h.viewer.current != 150 {
		t.Fatal("a drag closed or switched the viewer")
	}
	before = h.viewer.zoom.offset
	h.scroll(f32.Pt(400, 300), 1, 0)
	if h.viewer.current != 150 || h.viewer.zoom.offset.Y >= before.Y {
		t.Fatalf("wheel over an enlarged photo: current %d, offset %v → %v", h.viewer.current, before, h.viewer.zoom.offset)
	}

	// Ctrl+0 returns to the fitted photo; Ctrl+= zooms around the centre.
	h.router.Queue(key.Event{Name: "0", Modifiers: key.ModCtrl, State: key.Press})
	h.frame()
	h.settle()
	for i := 0; i < 60 && h.viewer.zoom.active(); i++ {
		h.frame()
	}
	if h.viewer.zoom.active() {
		t.Fatalf("Ctrl+0 left the photo zoomed: %+v", h.viewer.zoom)
	}
	h.router.Queue(key.Event{Name: "=", Modifiers: key.ModCtrl, State: key.Press})
	h.frame()
	h.settle()
	if !h.viewer.zoom.active() || h.viewer.zoom.offset != (f32.Point{}) {
		t.Fatalf("Ctrl+= zoomed off centre: %+v", h.viewer.zoom)
	}
	// Switching photos starts the next one fitted.
	h.click(790, 300)
	if h.viewer.current != 160 || h.viewer.zoom.active() {
		t.Fatalf("next photo %d zoomed: %v", h.viewer.current, h.viewer.zoom.active())
	}
}

func TestMediaViewerOpensInWindow(t *testing.T) {
	h := newViewerHarness(t)
	s := h.store
	var popped []model.Message
	var current model.Message
	h.viewer.popout = func(chat int64, m model.Message, known mediaList) { current, popped = m, known.items }
	h.viewer.Open(1, s.photos[14], s.photos)
	h.frame()
	h.click(720, 28)
	if current.Key.MessageID != 150 || len(popped) != len(s.photos) || h.viewer.open {
		t.Fatalf("window button: opened %d with %d photos, viewer still open: %v", current.Key.MessageID, len(popped), h.viewer.open)
	}

	// In a window of its own, the background does not close the viewer.
	h.viewer.popout = nil
	h.viewer.standalone = true
	h.viewer.Open(1, s.photos[0], s.photos)
	h.frame()
	h.click(20, 300) // no left side zone on the first photo: the background
	if !h.viewer.open {
		t.Fatal("a click on the background closed a standalone viewer")
	}
	h.click(768, 28)
	if h.viewer.open {
		t.Fatal("✕ did not close the standalone viewer")
	}
}
