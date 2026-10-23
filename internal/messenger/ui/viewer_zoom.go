// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"time"

	"gio-mw/widget/scroll"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"komarugram/internal/messenger/model"
)

const (
	// zoomStep is the scale change of one wheel notch, scroll.NotchPixels.
	zoomStep = 1.25
	zoomMax  = 8 // picture pixels shown 8 screen pixels wide at most
	// zoomTau is the time constant of the smooth zoom: the scale covers 63%
	// of the way to its target in that time, 95% in three times that.
	zoomTau = 70 * time.Millisecond
)

// viewerZoom places the picture on screen once it is zoomed. Scale is
// screen pixels per picture pixel; zero means the picture is fitted to the
// stage, as it opens. Offsets are of the picture's centre from the stage's
// centre.
type viewerZoom struct {
	scale, target float32
	offset, goal  f32.Point
	// While a zoom animates, point of the picture stays under anchor on screen,
	// so the picture grows around the pointer rather than drifting.
	anchor, point f32.Point
	anchored      bool
	last          time.Time

	pan struct {
		pressed, dragging bool
		id                pointer.ID
		last              f32.Point
		moved             float32
	}
	// wheel is the scroll, in pixels, not yet turned into a switch of
	// items; it outlives the switch, as a touchpad goes on.
	wheel float32
}

func (z *viewerZoom) reset() {
	*z = viewerZoom{wheel: z.wheel}
}

func (z *viewerZoom) active() bool { return z.scale > 0 }

// fitScale is the scale of a picture of native size fitted into stage, never
// above its own pixels.
func fitScale(native, stage image.Point) float32 {
	if native.X <= 0 || native.Y <= 0 {
		return 1
	}
	return min(1, float32(stage.X)/float32(native.X), float32(stage.Y)/float32(native.Y))
}

// zoomBy multiplies the target scale by factor around at, a point relative
// to the stage centre.
func (z *viewerZoom) zoomBy(factor float32, at f32.Point, fit float32) {
	if !z.active() {
		z.scale, z.target = fit, fit
		z.offset, z.goal = f32.Point{}, f32.Point{}
	}
	z.point = at.Sub(z.offset).Div(z.scale)
	z.anchor, z.anchored = at, true
	z.target = min(max(z.target*factor, fit), max(fit, zoomMax))
	z.goal = at.Sub(z.point.Mul(z.target))
}

// step advances the animation to now and reports whether it goes on. With
// animations off, it jumps to the target.
func (z *viewerZoom) step(now time.Time, animate bool, fit float32, native, view image.Point) bool {
	if !z.active() {
		z.last = now
		return false
	}
	k := float32(1)
	if animate {
		dt := min(now.Sub(z.last), 50*time.Millisecond)
		if z.last.IsZero() {
			dt = time.Second / 60
		}
		k = 1 - float32(math.Exp(-float64(dt)/float64(zoomTau)))
	}
	z.last = now
	// Geometric steps feel even: 1→2 takes as long as 2→4.
	z.scale *= float32(math.Pow(float64(z.target/z.scale), float64(k)))
	if z.anchored {
		z.offset = z.anchor.Sub(z.point.Mul(z.scale))
	} else {
		z.offset = z.offset.Add(z.goal.Sub(z.offset).Mul(k))
	}
	z.offset = clampOffset(z.offset, z.scale, native, view)
	z.goal = clampOffset(z.goal, z.target, native, view)
	near := math.Abs(math.Log(float64(z.scale/z.target))) < 1e-3
	d := z.goal.Sub(z.offset)
	if !near || d.X*d.X+d.Y*d.Y > .25 {
		return true
	}
	z.scale, z.offset, z.anchored = z.target, z.goal, false
	if z.target <= fit*1.0001 {
		// Back at the fitted size: follow the window again.
		z.scale, z.target = 0, 0
		z.offset, z.goal = f32.Point{}, f32.Point{}
	}
	return false
}

// unzoom animates back to the fitted size.
func (z *viewerZoom) unzoom(fit float32) {
	if z.active() {
		z.target, z.goal, z.anchored = fit, f32.Point{}, false
	}
}

// native is the picture's own size, or zero when its metadata lacks it;
// such a picture is shown fitted and cannot be zoomed.
func native(m model.Message) image.Point {
	return image.Pt(max(m.Media.Width, 0), max(m.Media.Height, 0))
}

// clampOffset keeps an enlarged picture covering the view, and one that fits
// centred in it.
func clampOffset(o f32.Point, scale float32, native, view image.Point) f32.Point {
	clampAxis := func(o, size, view float32) float32 {
		half := max(0, (size-view)/2)
		return min(max(o, -half), half)
	}
	return f32.Pt(
		clampAxis(o.X, float32(native.X)*scale, float32(view.X)),
		clampAxis(o.Y, float32(native.Y)*scale, float32(view.Y)),
	)
}

// overflows reports whether an enlarged picture is larger than the view, so
// that it can be moved.
func (z *viewerZoom) overflows(native, view image.Point) bool {
	return z.active() && (float32(native.X)*z.target > float32(view.X)+.5 || float32(native.Y)*z.target > float32(view.Y)+.5)
}

func (z *viewerZoom) panBy(d f32.Point, native, view image.Point) {
	z.anchored = false
	z.offset = clampOffset(z.offset.Add(d), z.scale, native, view)
	z.goal = clampOffset(z.goal.Add(d), z.target, native, view)
}

// zoomArea registers the pointer handler over the picture's view. It passes
// the pointer on, so clicks still reach the side zones and the backdrop; a
// drag takes the pointer, so it clicks nothing.
func (v *mediaViewer) zoomArea(gtx layout.Context, view image.Rectangle, native image.Point) {
	z := &v.zoom
	area := clip.Rect(view).Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	if z.overflows(native, view.Size()) {
		if z.pan.dragging {
			pointer.CursorGrabbing.Add(gtx.Ops)
		} else {
			pointer.CursorGrab.Add(gtx.Ops)
		}
	}
	event.Op(gtx.Ops, z)
	pass.Pop()
	area.Pop()
}

// zoomEvents handles the pointer over the picture's view: Ctrl with the wheel
// (or a pinch, which touchpads send as Ctrl and scroll) zooms, the wheel
// switches items or moves an enlarged one, and a drag moves it. It runs
// before anything is drawn, since it may change the item.
func (v *mediaViewer) zoomEvents(gtx layout.Context, items []model.Message, i int, view image.Rectangle, native image.Point, fit float32) {
	z := &v.zoom
	center := layout.FPt(view.Min.Add(view.Max)).Mul(.5)
	slop := float32(gtx.Dp(6))
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target:  z,
			Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Scroll,
			ScrollX: pointer.ScrollRange{Min: math.MinInt32, Max: math.MaxInt32},
			ScrollY: pointer.ScrollRange{Min: math.MinInt32, Max: math.MaxInt32},
		})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			if e.Buttons == pointer.ButtonPrimary && z.overflows(native, view.Size()) {
				z.pan.pressed, z.pan.dragging, z.pan.id, z.pan.last, z.pan.moved = true, false, e.PointerID, e.Position, 0
			}
		case pointer.Drag:
			if !z.pan.pressed || e.PointerID != z.pan.id {
				break
			}
			d := e.Position.Sub(z.pan.last)
			z.pan.last = e.Position
			z.pan.moved += float32(math.Hypot(float64(d.X), float64(d.Y)))
			if !z.pan.dragging && z.pan.moved > slop {
				z.pan.dragging = true
				gtx.Execute(pointer.GrabCmd{Tag: z, ID: e.PointerID})
			}
			if z.pan.dragging {
				z.panBy(d, native, view.Size())
			}
		case pointer.Release, pointer.Cancel:
			z.pan.pressed, z.pan.dragging = false, false
		case pointer.Scroll:
			v.wheel(gtx, e, items, i, center, native, view.Size(), fit)
			i = indexOf(items, v.current)
		}
	}
}

func (v *mediaViewer) wheel(gtx layout.Context, e pointer.Event, items []model.Message, i int, center f32.Point, native, view image.Point, fit float32) {
	z := &v.zoom
	d := scroll.Pixels(e)
	if e.Modifiers.Contain(key.ModShortcut) {
		if native == (image.Point{}) {
			return
		}
		factor := float32(math.Pow(zoomStep, float64(-d.Y/scroll.NotchPixels)))
		z.zoomBy(min(max(factor, .5), 2), e.Position.Sub(center), fit)
		return
	}
	if z.overflows(native, view) {
		z.panBy(d.Mul(-1), native, view)
		return
	}
	// An item for each notch of the wheel, at once however fast it turns, as
	// in Telegram Desktop, and for each notch's distance on a touchpad.
	step := d.Y
	if math.Abs(float64(d.X)) > math.Abs(float64(step)) {
		step = d.X
	}
	if step*z.wheel < 0 {
		z.wheel = 0 // a change of direction starts over
	}
	z.wheel += step
	n := int(z.wheel / scroll.NotchPixels)
	if n == 0 {
		return
	}
	if scroll.IsWheel(e) {
		// A notch larger than NotchPixels, as on KDE Plasma, is one item
		// still; what is left of it would add up to skipped items.
		z.wheel = 0
	} else {
		z.wheel -= float32(n) * scroll.NotchPixels
	}
	v.show(items, min(max(i+n, 0), len(items)-1), true)
}
