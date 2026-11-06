// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
)

// showing reports the text of the tooltip shown, empty for none.
func (t *tooltips) showing() string {
	if t.over == (tooltipTag{}) || t.expired || t.shownAt.IsZero() {
		return ""
	}
	return t.text
}

func (h *viewerHarness) move(x, y float32) {
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(x, y), Time: time.Duration(h.now.UnixNano())})
	h.frame()
}

// frames draws frames for d.
func (h *viewerHarness) frames(d time.Duration) {
	for end := h.now.Add(d); h.now.Before(end); {
		h.frame()
	}
}

// A button's tooltip shows as Chromium's does: half a second after the pointer
// came over it, until a press, a key, the pointer leaving, or ten seconds; what was
// hidden over a button stays so until the pointer goes to another.
func TestViewerTooltips(t *testing.T) {
	h := newViewerHarness(t)
	s := h.store
	h.viewer.Open(1, s.photos[14], s.photos[13:16])
	h.until("the gallery pages", func() bool { return h.count() == len(s.photos) })
	tips := &h.viewer.tips

	h.move(764, 286)
	h.frames(400 * time.Millisecond)
	if got := tips.showing(); got != "" {
		t.Fatalf("shown at once: %q", got)
	}
	h.frames(150 * time.Millisecond)
	if got := tips.showing(); got != "Next" {
		t.Fatalf("after half a second: %q", got)
	}
	// Moving over the button does not start it again.
	h.move(760, 300)
	if got := tips.showing(); got != "Next" {
		t.Fatalf("moved: %q", got)
	}
	// A press hides it, and it stays hidden over the button.
	h.click(760, 300)
	h.frames(time.Second)
	if got := tips.showing(); got != "" {
		t.Fatalf("pressed: %q", got)
	}
	// Away and back, it shows again; the close button's tells what it does.
	h.move(768, 28)
	h.frames(600 * time.Millisecond)
	if got := tips.showing(); got != "Close" {
		t.Fatalf("over close: %q", got)
	}
	// A key hides it.
	h.key(key.NameRightArrow)
	if got := tips.showing(); got != "" {
		t.Fatalf("after a key: %q", got)
	}
	h.move(400, 300)
	h.move(764, 286)
	h.frames(600 * time.Millisecond)
	if tips.showing() == "" {
		t.Fatal("not shown again")
	}
	// Ten seconds, and it goes.
	h.now = h.now.Add(10 * time.Second)
	h.frame()
	if got := tips.showing(); got != "" {
		t.Fatalf("after ten seconds: %q", got)
	}
	// Leaving takes it away.
	h.move(400, 300)
	if tips.over != (tooltipTag{}) {
		t.Fatal("still over a button")
	}
}

// It goes below and right of the pointer, slid back into the window, and above
// the pointer where there is no room below.
func TestTooltipPlace(t *testing.T) {
	off := image.Pt(10, 15)
	bounds := image.Pt(800, 600)
	size := image.Pt(100, 24)
	for _, c := range []struct {
		pointer, want image.Point
	}{
		{image.Pt(100, 100), image.Pt(110, 115)},
		{image.Pt(780, 100), image.Pt(700, 115)},
		{image.Pt(100, 590), image.Pt(110, 566)},
		{image.Pt(790, 595), image.Pt(700, 571)},
	} {
		if got := tooltipPlace(c.pointer, size, bounds, off); got != c.want {
			t.Errorf("at %v: %v, want %v", c.pointer, got, c.want)
		}
	}
}

// The editor's buttons tell what they do, one that cannot be pressed now too, as
// Chromium's buttons do: Undo, before anything was done.
func TestEditorTooltips(t *testing.T) {
	h := newViewerEditHarness(t, ownPhoto)
	e := h.load()
	if e.scene.CanUndo() {
		t.Fatal("something to undo already")
	}
	tips := &h.v.tips
	// Along the bottom row, to Undo.
	y := float32(h.size.Y - 28)
	found := false
	for x := float32(0); x < float32(h.size.X) && !found; x += 4 {
		h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(x, y)})
		h.frame()
		found = tips.over == tooltipTag{&e.undo}
	}
	if !found {
		t.Fatal("no Undo under the pointer")
	}
	time.Sleep(tooltipShowDelay + 100*time.Millisecond)
	h.frame()
	if got := tips.showing(); got != "Отменить" {
		t.Fatalf("over Undo: %q", got)
	}
	// A key hides it.
	h.key(key.NameEscape, 0)
	if got := tips.showing(); got != "" {
		t.Fatalf("after a key: %q", got)
	}
}
