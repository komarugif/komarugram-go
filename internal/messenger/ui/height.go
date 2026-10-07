// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"image"
	"math"
)

const heightDuration = token.DurationMedium2

// heightTransition keeps layout, paint and hit testing at the same height.
// First layout and width changes snap; toggles can reverse mid-transition.
// State belongs to the view, never to the frame or a global widget registry.
type heightTransition struct {
	tween         wdk.FloatTween
	width         int
	value, target int
	initialized   bool
}

func (h *heightTransition) Value(gtx layout.Context, target int, animate bool) int {
	target = max(0, target)
	width := gtx.Constraints.Max.X
	if !h.initialized || h.width != width || !animate || !wdk.AnimationsEnabled(gtx) {
		h.tween = wdk.FloatTween{}
	}
	h.initialized, h.width, h.target = true, width, target
	h.tween.Duration, h.tween.Easing = heightDuration, &token.EasingStandard
	h.value = max(0, int(math.Round(float64(h.tween.Animate(gtx, float32(target))))))
	return h.value
}

// Card measures its content once and paints the shared card at its animated
// height. Its clip applies to event handlers as well as drawing operations.
func (h *heightTransition) Card(gtx layout.Context, content layout.Widget, padding unit.Dp) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	inner := gtx
	inner.Constraints.Min = image.Pt(gtx.Constraints.Max.X, 0)
	dims := layout.UniformInset(padding).Layout(inner, content)
	call := macro.Stop()
	dims.Size.Y = h.Value(gtx, dims.Size.Y, true)
	fillRounded(gtx, scheme(gtx).Surface.Color, dims.Size, gtx.Dp(16))
	area := clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(16)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	area.Pop()
	return dims
}
