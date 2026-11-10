// SPDX-License-Identifier: Unlicense OR MIT

package scroll

import (
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
)

// middle sends an event of the middle button: pressed for a press and a
// drag, which the platforms send as a move with the button down.
func (h *harness) middle(kind pointer.Kind, x, y float32) {
	e := pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(x, y)}
	if kind == pointer.Press || kind == pointer.Drag {
		e.Buttons = pointer.ButtonTertiary
	}
	if kind == pointer.Drag {
		e.Kind = pointer.Move
	}
	h.r.Queue(e)
	h.frame()
}

// A click of the middle button leaves the list scrolling by the pointer:
// not within the dead zone, down below the mark, faster farther, up above
// it; any press ends it.
func TestMiddleClickScrollsByThePointer(t *testing.T) {
	h := newHarness(500, 64)
	h.event(pointer.Move, 150, 300)
	h.middle(pointer.Press, 150, 300)
	h.middle(pointer.Release, 150, 300)
	if !h.l.Autoscrolling() {
		t.Fatal("a click of the middle button did not start the scrolling")
	}
	h.middle(pointer.Move, 150, 310)
	start := h.scrolled()
	for range 10 {
		h.frame()
	}
	if h.scrolled() != start {
		t.Fatalf("within the dead zone the list scrolled by %d", h.scrolled()-start)
	}
	h.middle(pointer.Move, 150, 360)
	start = h.scrolled()
	for range 10 {
		h.frame()
	}
	slow := h.scrolled() - start
	if slow <= 0 {
		t.Fatalf("below the mark the list scrolled by %d", slow)
	}
	h.middle(pointer.Move, 150, 600)
	start = h.scrolled()
	for range 10 {
		h.frame()
	}
	if fast := h.scrolled() - start; fast <= 2*slow {
		t.Fatalf("far below the mark the list scrolled by %d, near it by %d", fast, slow)
	}
	h.middle(pointer.Move, 150, 100)
	start = h.scrolled()
	for range 10 {
		h.frame()
	}
	if h.scrolled() >= start {
		t.Fatalf("above the mark the list scrolled by %d", h.scrolled()-start)
	}
	h.event(pointer.Press, 150, 100)
	h.event(pointer.Release, 150, 100)
	if h.l.Autoscrolling() {
		t.Fatal("a press did not end the scrolling")
	}
	start = h.scrolled()
	for range 10 {
		h.frame()
	}
	if h.scrolled() != start {
		t.Fatal("the list scrolled on after the scrolling ended")
	}
}

// The middle button dragged away scrolls while it is down, and lets go
// of the list with it.
func TestMiddleDragScrollsWhileDown(t *testing.T) {
	h := newHarness(500, 64)
	h.event(pointer.Move, 150, 300)
	h.middle(pointer.Press, 150, 300)
	h.middle(pointer.Drag, 150, 450)
	start := h.scrolled()
	for range 10 {
		h.frame()
	}
	if h.scrolled() <= start {
		t.Fatal("dragged down, the list did not scroll")
	}
	h.middle(pointer.Release, 150, 450)
	if h.l.Autoscrolling() {
		t.Fatal("let go after a drag, the button left the list scrolling")
	}
}

// An element that takes the middle button for itself keeps its press from
// starting the scrolling; the wheel ends it.
func TestAutoscrollCancelledAndWheel(t *testing.T) {
	h := newHarness(500, 64)
	h.event(pointer.Move, 150, 300)
	h.middle(pointer.Press, 150, 300)
	h.l.CancelAutoscroll()
	h.middle(pointer.Release, 150, 300)
	if h.l.Autoscrolling() {
		t.Fatal("cancelled, the press started the scrolling")
	}
	h.middle(pointer.Press, 150, 300)
	h.middle(pointer.Release, 150, 300)
	h.r.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(150, 300), Scroll: f32.Pt(0, 100), Wheel: true})
	h.frame()
	if h.l.Autoscrolling() {
		t.Fatal("the wheel did not end the scrolling")
	}
}
