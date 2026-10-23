// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestRevealButtonWidensOnHover rests the pointer on a button: it widens
// to its text over some frames, not at once, and narrows to its icon once
// the pointer leaves.
func TestRevealButtonWidensOnHover(t *testing.T) {
	var r input.Router
	var b revealButton
	now := time.Now()
	frame := func() int {
		gtx := layout.Context{Ops: new(op.Ops), Source: r.Source(), Now: now, Constraints: layout.Exact(image.Pt(600, 100)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		width := b.Layout(gtx, iconGift, "Send a gift", scheme(gtx).Primary.Color).Size.X
		r.Frame(gtx.Ops)
		return width
	}
	closed := frame()
	if closed != 40 {
		t.Fatalf("closed: %d px, want the icon's 40", closed)
	}
	r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(20, 20)})
	// The hover is seen in the frame after, where the widening starts.
	frame()
	now = now.Add(frameStep)
	frame()
	now = now.Add(frameStep)
	opening := frame()
	for range 60 {
		now = now.Add(frameStep)
		frame()
	}
	open := frame()
	if !(closed < opening && opening < open) {
		t.Fatalf("widths %d, %d, %d: not widened over frames", closed, opening, open)
	}
	r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(590, 90)})
	frame()
	for range 60 {
		now = now.Add(frameStep)
		frame()
	}
	if got := frame(); got != closed {
		t.Fatalf("after the pointer left: %d px", got)
	}
}

// frameStep is a frame at 60 frames a second.
const frameStep = 16 * time.Millisecond

// hoverLabel moves the pointer onto the middle of the element labeled
// label in the frame r saw last.
func hoverLabel(t *testing.T, r *input.Router, label string) {
	t.Helper()
	var find func([]input.SemanticNode) (image.Rectangle, bool)
	find = func(nodes []input.SemanticNode) (image.Rectangle, bool) {
		for _, n := range nodes {
			if n.Desc.Label == label {
				return n.Desc.Bounds, true
			}
			if b, ok := find(n.Children); ok {
				return b, true
			}
		}
		return image.Rectangle{}, false
	}
	bounds, ok := find(r.AppendSemantics(nil))
	if !ok {
		t.Fatalf("no element labeled %q", label)
	}
	at := bounds.Min.Add(bounds.Size().Div(2))
	r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(at.X), float32(at.Y))})
}
