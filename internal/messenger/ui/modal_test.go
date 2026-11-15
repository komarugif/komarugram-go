// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"

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

// modalHarness lays out a dialog over another one, both 100 px squares in
// the middle of a 400 px window. A zero gtx.Now makes animations finish at
// once.
type modalHarness struct {
	router       input.Router
	below, above modal
	locked       bool
	// shownBelow and shownAbove are what Layout reported last.
	shownBelow, shownAbove bool
}

func (h *modalHarness) frame() {
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Source: h.router.Source(), Constraints: layout.Exact(image.Pt(400, 400)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	square := func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(100, 100)} }
	// The owner of both updates the upper one first.
	h.above.Update(gtx, h.locked)
	h.shownBelow = h.below.Layout(gtx, h.above.Shown(), square)
	h.shownAbove = h.above.Layout(gtx, h.locked, square)
	h.router.Frame(ops)
}

func (h *modalHarness) click(x, y float32) {
	h.router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y)},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(x, y)},
	)
	h.frame()
	h.frame()
}

func (h *modalHarness) escape() {
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame()
	h.frame()
}

func TestModalCloses(t *testing.T) {
	var h modalHarness
	h.below.Open()
	h.frame()
	h.click(200, 200)
	if !h.shownBelow {
		t.Fatal("a click on the dialog closed it")
	}
	h.click(10, 10)
	if h.shownBelow || h.below.Shown() {
		t.Fatal("a click outside did not close the dialog")
	}
	h.below.Open()
	h.frame()
	h.escape()
	if h.shownBelow {
		t.Fatal("Escape did not close the dialog")
	}
}

func TestModalNestedAndLocked(t *testing.T) {
	var h modalHarness
	h.below.Open()
	h.above.Open()
	h.frame()
	h.locked = true
	h.escape()
	h.click(10, 10)
	if !h.shownAbove || !h.shownBelow {
		t.Fatal("a locked dialog closed")
	}
	h.locked = false
	h.escape()
	if h.shownAbove || !h.shownBelow {
		t.Fatalf("Escape closed below %v, above %v; want only the upper dialog", !h.shownBelow, !h.shownAbove)
	}
	h.escape()
	if h.shownBelow {
		t.Fatal("Escape did not close the lower dialog next")
	}
}

func TestModalBack(t *testing.T) {
	var h modalHarness
	backs := 0
	h.below.back = func() bool {
		backs++
		return backs == 1
	}
	h.below.Open()
	h.frame()
	h.escape()
	if !h.shownBelow || backs != 1 {
		t.Fatal("Escape did not go back first")
	}
	h.escape()
	if h.shownBelow {
		t.Fatal("Escape did not close the dialog once there was no going back")
	}
}
