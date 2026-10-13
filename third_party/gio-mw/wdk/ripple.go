// SPDX-License-Identifier: Unlicense OR MIT

package wdk

import (
	"image"
	"math"
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// Ripple timing, modeled on the Compose Material 3 ripple.
const (
	rippleExpandDuration  = 225 * time.Millisecond
	rippleFadeInDuration  = 75 * time.Millisecond
	rippleFadeOutDuration = 150 * time.Millisecond
	rippleStartRadius     = 0.3 // Of the larger bounds dimension.
)

// rippleEasing is FastOutSlowIn.
var rippleEasing = token.Easing{X1: 0.4, Y1: 0, X2: 0.2, Y2: 1}

// Ripple draws the Material press ripple: a circle that spreads from the
// press position over the bounds and fades out after the release.
//
// Ripples replace the pressed state layer; widgets should draw the pressed
// state layer only when RipplesEnabled is false.
type Ripple struct {
	// Color is the ripple color including the pressed state layer opacity.
	Color token.MatColor
	// Bounds is the area the ripple spreads over, in the same coordinates as
	// the press positions unless Centered is set.
	Bounds image.Rectangle
	// Clip makes the shape the ripple is clipped to, usually the state
	// layer's. It is called only when a ripple is visible: a shape recorded
	// for every widget in every frame, unused but for a second after a
	// press, made most of the operations of a row in a list.
	Clip func() clip.Op
	// Centered starts the ripple at the center of Bounds instead of at the
	// press position, for small targets such as radio buttons.
	Centered bool
}

// RipplesEnabled reports whether ripples are drawn in this frame.
func RipplesEnabled(gtx layout.Context) bool {
	return AnimationsEnabled(gtx) && !gtx.Now.IsZero()
}

// Draw draws a ripple for each press and requests the next frame while any
// of them is still visible.
func (r Ripple) Draw(gtx layout.Context, presses []widget.Press) {
	if !RipplesEnabled(gtx) || r.Color.A == 0 || r.Bounds.Empty() {
		return
	}
	size := layout.FPt(r.Bounds.Size())
	center := layout.FPt(r.Bounds.Min).Add(size.Mul(0.5))
	// The ripple ends up covering the bounds from their center.
	endRadius := float32(math.Hypot(float64(size.X), float64(size.Y))) / 2
	startRadius := max(size.X, size.Y) * rippleStartRadius
	if r.Centered {
		endRadius = max(size.X, size.Y) / 2
	}

	active := false
	var shape clip.Op
	shaped := false
	for _, p := range presses {
		alpha, running := rippleAlpha(gtx.Now, p)
		active = active || running
		if alpha <= 0 {
			continue
		}
		expand := float32(rippleEasing.Ease(float64(progress(gtx.Now.Sub(p.Start), rippleExpandDuration))))
		origin := center
		if !r.Centered {
			origin = layout.FPt(p.Position)
		}
		c := origin.Add(center.Sub(origin).Mul(expand))
		radius := startRadius + (endRadius-startRadius)*expand

		color := r.Color
		color.A = uint8(float32(color.A)*alpha + 0.5)
		if !shaped {
			shape, shaped = r.Clip(), true
		}
		stack := shape.Push(gtx.Ops)
		circle := clip.Ellipse{
			Min: image.Pt(int(c.X-radius), int(c.Y-radius)),
			Max: image.Pt(int(math.Ceil(float64(c.X+radius))), int(math.Ceil(float64(c.Y+radius)))),
		}
		paint.FillShape(gtx.Ops, color.AsNRGBA(), circle.Op(gtx.Ops))
		stack.Pop()
	}
	if active {
		gtx.Execute(op.InvalidateCmd{})
	}
}

// rippleAlpha returns the opacity factor of the ripple for press p at now,
// and whether the ripple still changes over time.
func rippleAlpha(now time.Time, p widget.Press) (float32, bool) {
	alpha := progress(now.Sub(p.Start), rippleFadeInDuration)
	if p.End.IsZero() {
		// Held down: fully visible once it has faded and spread in.
		return alpha, now.Sub(p.Start) < rippleExpandDuration
	}
	// The fade out starts after the release, but not before the fade in has
	// finished, so that even quick taps show a ripple.
	fadeOutStart := p.End
	if minStart := p.Start.Add(rippleFadeInDuration); fadeOutStart.Before(minStart) {
		fadeOutStart = minStart
	}
	if p.Cancelled {
		fadeOutStart = p.End
	}
	fadeOut := progress(now.Sub(fadeOutStart), rippleFadeOutDuration)
	return alpha * (1 - fadeOut), fadeOut < 1
}

// progress returns elapsed/total clamped to [0, 1].
func progress(elapsed, total time.Duration) float32 {
	if elapsed <= 0 {
		return 0
	}
	if elapsed >= total {
		return 1
	}
	return float32(elapsed) / float32(total)
}
