// SPDX-License-Identifier: Unlicense OR MIT

package scroll

import (
	"image"
	"math"
	"time"

	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// A press of the middle button on a list scrolls it by the pointer, as
// Chromium's autoscroll does: a mark stands where it was pressed, and the
// farther the pointer goes from it past a dead zone, the faster the list
// scrolls that way. Pressed and let go where it was, the button leaves the
// list scrolling until any button is pressed or the wheel turns; dragged
// away first, it stops the scrolling when it is let go.

const (
	// autoDeadZone is how far the pointer goes from the mark before the
	// list scrolls.
	autoDeadZone = unit.Dp(15)
	// autoMarkSize is how large the mark is.
	autoMarkSize = unit.Dp(28)
	// autoMaxSpeed is the fastest the list scrolls, in pixels a second.
	autoMaxSpeed = 20000
	// autoMaxStep is the longest time between frames the scrolling takes
	// as it is: a frame late after a pause does not jump.
	autoMaxStep = 50 * time.Millisecond
)

// autoscroll is a List's scrolling by the middle button.
type autoscroll struct {
	// active is set while the list scrolls by the pointer; held while the
	// button that started it is down, and dragged once the pointer went
	// past the dead zone since.
	active, held, dragged bool
	// origin is where the button was pressed, and at where the pointer
	// is, in the list's coordinates.
	origin, at f32.Point
	// last is the time of the frame that scrolled last.
	last time.Time
	// area is the tag of the area that takes the pointer while it scrolls,
	// over all that the list shows.
	area byte
}

// CancelAutoscroll ends the scrolling a press of the middle button
// started, or keeps that press from starting it: an element the list
// shows that takes the middle button for itself, as a link opens in
// Chromium, calls it when it takes the press, in the same frame.
func (l *List) CancelAutoscroll() {
	l.auto = autoscroll{}
}

// Autoscrolling tells whether the list scrolls by the middle button.
func (l *List) Autoscrolling() bool { return l.auto.active }

// autoscrollEvent takes pointer event e, of the list's area or of the
// autoscroll's own, for the scrolling by the middle button.
func (l *List) autoscrollEvent(gtx layout.Context, e pointer.Event) {
	a := &l.auto
	switch e.Kind {
	case pointer.Press:
		if a.active {
			// Any button ends it; this one does nothing else.
			l.CancelAutoscroll()
			return
		}
		if e.Source != pointer.Mouse || e.Buttons != pointer.ButtonTertiary {
			return
		}
		l.stopWheel()
		l.stopPageGlide()
		*a = autoscroll{active: true, held: true, origin: e.Position, at: e.Position, last: gtx.Now}
	case pointer.Move, pointer.Drag:
		if !a.active {
			return
		}
		a.at = e.Position
		if a.held && l.autoDistance(gtx) > 0 {
			a.dragged = true
		}
	case pointer.Release:
		if !a.active || !a.held {
			return
		}
		a.held = false
		a.at = e.Position
		if a.dragged {
			l.CancelAutoscroll()
		}
	case pointer.Scroll, pointer.Cancel:
		if a.active {
			l.CancelAutoscroll()
		}
	}
}

// autoDistance is how far past the dead zone the pointer is from the mark
// along the list, with its sign: down or right is more than 0.
func (l *List) autoDistance(gtx layout.Context) float32 {
	d := l.auto.at.Sub(l.auto.origin)
	along := l.mainF(d.X, d.Y)
	dead := float32(gtx.Dp(autoDeadZone))
	if math.Abs(float64(along)) <= float64(dead) {
		return 0
	}
	if along < 0 {
		return along + dead
	}
	return along - dead
}

// autoSpeed is how fast, in pixels a second, the list scrolls with the
// pointer d pixels past the dead zone: slowly near it, for reading, and
// much faster far from it, for going a long way.
func autoSpeed(m unit.Metric, d float32) float32 {
	dp := float32(math.Abs(float64(d))) / m.PxPerDp
	speed := (8*dp + dp*dp/25) * m.PxPerDp
	speed = min(speed, autoMaxSpeed)
	if d < 0 {
		return -speed
	}
	return speed
}

// applyAutoscroll moves the list by what the scrolling by the middle
// button does in this frame, and asks for the next while it goes on.
func (l *List) applyAutoscroll(gtx layout.Context) {
	a := &l.auto
	if !a.active {
		return
	}
	step := min(gtx.Now.Sub(a.last), autoMaxStep)
	a.last = gtx.Now
	if d := l.autoDistance(gtx); d != 0 && step > 0 {
		l.addOffset(autoSpeed(gtx.Metric, d) * float32(step.Seconds()))
		l.lastActive = gtx.Now
	}
	gtx.Execute(op.InvalidateCmd{})
}

// layoutAutoscroll takes the pointer over all of the list, size large,
// while it scrolls by the middle button, so that nothing it shows is
// clicked meanwhile, and draws the mark where the button was pressed.
func (l *List) layoutAutoscroll(gtx layout.Context, size image.Point) {
	a := &l.auto
	if !a.active {
		return
	}
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &a.area)
	pointer.CursorAllScroll.Add(gtx.Ops)
	area.Pop()

	side := gtx.Dp(autoMarkSize)
	sc := wdk.GetMaterialTheme(gtx).Scheme
	at := image.Pt(int(a.origin.X)-side/2, int(a.origin.Y)-side/2)
	defer op.Offset(at).Push(gtx.Ops).Pop()
	circle := image.Rectangle{Max: image.Pt(side, side)}
	paint.FillShape(gtx.Ops, sc.Surface.OnColor.SetOpacity(.38).AsNRGBA(), clip.Ellipse(circle).Op(gtx.Ops))
	inner := circle.Inset(gtx.Dp(1))
	paint.FillShape(gtx.Ops, sc.Surface.Color.AsNRGBA(), clip.Ellipse(inner).Op(gtx.Ops))
	ink := sc.Surface.OnColor.AsNRGBA()
	c := float32(side) / 2
	dot := float32(gtx.Dp(2))
	paint.FillShape(gtx.Ops, ink, clip.Ellipse(image.Rect(int(c-dot), int(c-dot), int(c+dot+.5), int(c+dot+.5))).Op(gtx.Ops))
	// An arrow at each end the list scrolls to.
	half, tall, gap := float32(gtx.Dp(4)), float32(gtx.Dp(4)), float32(gtx.Dp(5))
	for _, dir := range []float32{-1, 1} {
		var p clip.Path
		p.Begin(gtx.Ops)
		tip := c + dir*(gap+tall)
		base := c + dir*gap
		if l.Axis == layout.Horizontal {
			p.MoveTo(f32.Pt(tip, c))
			p.LineTo(f32.Pt(base, c-half))
			p.LineTo(f32.Pt(base, c+half))
		} else {
			p.MoveTo(f32.Pt(c, tip))
			p.LineTo(f32.Pt(c-half, base))
			p.LineTo(f32.Pt(c+half, base))
		}
		p.Close()
		paint.FillShape(gtx.Ops, ink, clip.Outline{Path: p.End()}.Op())
	}
}
