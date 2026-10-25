// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"

	"gio-mw/widget/scroll"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
)

// dragHorizontalStrip shares mouse dragging and wheel scrolling between the
// media viewer and composer pack strip. Touch scrolling remains in layout.List.
func dragHorizontalStrip(gtx layout.Context, width int, drag *stripDrag, list *layout.List, invalidate func()) {
	horizontalStripInput(gtx, width, drag, list, invalidate, true)
}

// dragOnlyHorizontalStrip lets a mouse drag the strip, as
// dragHorizontalStrip, and leaves the wheel to what holds it, as the list
// of chats the row of stories goes up with.
func dragOnlyHorizontalStrip(gtx layout.Context, width int, drag *stripDrag, list *layout.List, invalidate func()) {
	horizontalStripInput(gtx, width, drag, list, invalidate, false)
}

// horizontalStripInput drags the strip, and scrolls it with the wheel when
// wheel is set.
func horizontalStripInput(gtx layout.Context, width int, drag *stripDrag, list *layout.List, invalidate func(), wheel bool) {
	area := clip.Rect{Max: image.Pt(width, gtx.Constraints.Max.Y)}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, drag)
	pass.Pop()
	area.Pop()
	slop := float32(gtx.Dp(6))
	filter := pointer.Filter{Target: drag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel}
	if wheel {
		filter.Kinds |= pointer.Scroll
		filter.ScrollY = pointer.ScrollRange{Min: math.MinInt32, Max: math.MaxInt32}
	}
	for {
		ev, ok := gtx.Event(filter)
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		d := drag
		switch e.Kind {
		case pointer.Press:
			if e.Source == pointer.Mouse && e.Buttons == pointer.ButtonPrimary {
				*d = stripDrag{pressed: true, id: e.PointerID, last: e.Position.X}
			}
		case pointer.Drag:
			if !d.pressed || e.PointerID != d.id {
				break
			}
			dx := e.Position.X - d.last
			d.last = e.Position.X
			d.moved += float32(math.Abs(float64(dx)))
			if !d.dragging && d.moved > slop {
				d.dragging = true
				gtx.Execute(pointer.GrabCmd{Tag: drag, ID: e.PointerID})
			}
			if d.dragging {
				scrollHorizontalStrip(drag, list, invalidate, -dx)
			}
		case pointer.Release, pointer.Cancel:
			d.pressed, d.dragging = false, false
		case pointer.Scroll:
			// As far as a list would scroll, the same on every platform.
			scrollHorizontalStrip(drag, list, invalidate, scroll.Pixels(e).Y)
		}
	}
}

// scrollStrip moves the strip by px, keeping fractions of a pixel for the
// next move. The list clamps the position to its ends when laid out.
func scrollHorizontalStrip(drag *stripDrag, list *layout.List, invalidate func(), px float32) {
	px += drag.rest
	whole := float32(math.Trunc(float64(px)))
	drag.rest = px - whole
	list.Position.Offset += int(whole)
	list.Position.BeforeEnd = true
	invalidate()
}
