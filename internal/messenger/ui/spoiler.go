// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// spoiler covers a value, such as a phone number in visual privacy mode,
// until it is clicked, then uncovers it with the circular wave of message
// spoilers. It stays uncovered until Hide.
type spoiler struct {
	click  gesture.Click
	shown  bool
	reveal spoilerReveal
}

// Hide covers the value again.
func (s *spoiler) Hide() {
	s.shown, s.reveal = false, spoilerReveal{}
}

// Layout draws w, covered if covered is set and the value has not been
// uncovered by a click. With animate unset the value uncovers at once.
func (s *spoiler) Layout(gtx layout.Context, covered, animate bool, w layout.Widget) layout.Dimensions {
	if !covered {
		return w(gtx)
	}
	for {
		e, ok := s.click.Update(gtx.Source)
		if !ok {
			break
		}
		if e.Kind == gesture.KindClick && !s.shown {
			s.shown = true
			s.reveal = spoilerReveal{started: gtx.Now, center: f32.Pt(float32(e.Position.X), float32(e.Position.Y)), pxPerDp: gtx.Metric.PxPerDp}
		}
	}
	// The cover is as wide as the value, not as the space around it.
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	if s.shown && (!animate || s.reveal.done(gtx.Now, dims.Size)) {
		call.Add(gtx.Ops)
		return dims
	}
	color := scheme(gtx).OutlineVariant.AsNRGBA()
	if s.shown {
		radius := s.reveal.radius(gtx.Now, dims.Size)
		paintSpoiler(gtx, dims.Size, s.reveal.center, radius, color, spoilerJoin{}, func() { call.Add(gtx.Ops) })
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
		return dims
	}
	// Nothing of the value is drawn while it is covered.
	paintSpoiler(gtx, dims.Size, f32.Point{}, 0, color, spoilerJoin{}, nil)
	area := clip.Rect(image.Rectangle{Max: dims.Size}).Push(gtx.Ops)
	pointer.CursorPointer.Add(gtx.Ops)
	s.click.Add(gtx.Ops)
	area.Pop()
	return dims
}
