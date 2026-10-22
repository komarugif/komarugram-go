// SPDX-License-Identifier: Unlicense OR MIT

package wdk

import (
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
)

// Default animation parameters used by the zero values of FloatTween and
// ColorTween.
const defaultTweenDuration = token.DurationShort4

var defaultTweenEasing = token.EasingStandard

// FloatTween animates a float32 value towards a target. Call Animate every
// frame with the desired value; when the target changes, the value moves
// from wherever it currently is to the new target.
//
// Animations can be turned off with SetAnimationsEnabled.
//
// The zero value is ready to use, animates with token.DurationShort4 and
// token.EasingStandard, and starts at the first target without animating.
type FloatTween struct {
	Duration time.Duration
	// Easing overrides token.EasingStandard when set.
	Easing *token.Easing
	tween[float32]
}

// Animate returns the value for the current frame and requests the next
// frame while the animation is running.
func (t *FloatTween) Animate(gtx layout.Context, target float32) float32 {
	return t.animate(gtx, target, t.Duration, t.Easing, lerpFloat)
}

// ColorTween animates a color like FloatTween animates a float32.
type ColorTween struct {
	Duration time.Duration
	// Easing overrides token.EasingStandard when set.
	Easing *token.Easing
	tween[token.MatColor]
}

// Animate returns the color for the current frame and requests the next
// frame while the animation is running.
func (t *ColorTween) Animate(gtx layout.Context, target token.MatColor) token.MatColor {
	return t.animate(gtx, target, t.Duration, t.Easing, token.LerpColor)
}

func lerpFloat(a, b float32, t float32) float32 {
	return a + (b-a)*t
}

type tween[V comparable] struct {
	from, to, value V
	start           time.Time
	started         bool
	// last is the time of the frame before; interrupted is set while the
	// animation runs to a target given while it ran to another.
	last        time.Time
	interrupted bool
}

func (tw *tween[V]) animate(gtx layout.Context, target V, duration time.Duration, easing *token.Easing, lerp func(a, b V, t float32) V) V {
	// Without a clock (gtx.Now is zero, e.g. in tests) there is nothing to
	// animate against, so jump to the target. The same applies when
	// animations are turned off, which also cuts running animations short.
	if !tw.started || gtx.Now.IsZero() || !AnimationsEnabled(gtx) {
		tw.started = true
		tw.from, tw.to, tw.value = target, target, target
		tw.last = gtx.Now
		return target
	}
	last := tw.last
	tw.last = gtx.Now
	if target != tw.to {
		moving := tw.value != tw.to
		tw.from, tw.to = tw.value, target
		tw.start, tw.interrupted = gtx.Now, moving
		if moving {
			// A value in motion keeps moving: timed from the frame
			// before, this frame moves it. Timed from this one, as an
			// animation from rest is, it stood still in every frame
			// with a new target: progress given every frame never
			// moved a bar.
			tw.start = last
		}
	}
	if tw.value == tw.to {
		tw.interrupted = false
		return tw.value
	}
	if duration <= 0 {
		duration = defaultTweenDuration
	}
	progress := float64(gtx.Now.Sub(tw.start)) / float64(duration)
	if progress >= 1 {
		tw.value, tw.interrupted = tw.to, false
		return tw.value
	}
	if easing == nil {
		easing = &defaultTweenEasing
		if tw.interrupted {
			// The standard easing starts at rest; a value in motion
			// would slow down to start again at each new target.
			easing = &token.EasingStandardDecelerate
		}
	}
	tw.value = lerp(tw.from, tw.to, float32(easing.Ease(progress)))
	gtx.Execute(op.InvalidateCmd{})
	return tw.value
}
