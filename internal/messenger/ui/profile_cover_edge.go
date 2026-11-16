// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// A cover with a background of its own does not end in a line: as
// Telegram Desktop's cover meets its buttons, which go across the whole
// width, the background goes on under the first cards, which stand apart,
// and fades into the info's own color there.
const (
	coverFadeHeight = unit.Dp(132)
	// coverFadeAboveTheEnd is how far above the cover's end the fade
	// begins: below the status, which stays on the whole background.
	coverFadeAboveTheEnd = unit.Dp(8)
)

// coverBackgroundHeight is how far down the background of a cover of
// height goes.
func coverBackgroundHeight(gtx layout.Context, height int) int {
	return height + gtx.Dp(coverFadeHeight)
}

// layoutCoverFade draws, over the cover's background of width, the info's
// own color coming in from just above the cover's end, height, to the end
// of the background: in an ease, so that the background has no line where
// it ends.
func layoutCoverFade(gtx layout.Context, width, height int, under color.NRGBA) {
	from := float32(height - gtx.Dp(coverFadeAboveTheEnd))
	to := float32(coverBackgroundHeight(gtx, height))
	// A linear gradient is linear: a smoothstep is drawn in pieces.
	const pieces = 8
	alpha := func(t float32) uint8 {
		s := t * t * (3 - 2*t)
		return uint8(float32(under.A)*s + .5)
	}
	// The pieces meet on whole pixels: overlapping, a row would be painted
	// twice, and show as a line.
	row := func(i int) int { return int(math.Round(float64(from + (to-from)*float32(i)/pieces))) }
	for i := range pieces {
		y0, y1 := row(i), row(i+1)
		if y1 <= y0 {
			continue
		}
		c0, c1 := under, under
		c0.A = alpha((float32(y0) - from) / (to - from))
		c1.A = alpha(min(1, (float32(y1)-from)/(to-from)))
		stack := clip.Rect(image.Rect(0, y0, width, y1)).Push(gtx.Ops)
		paint.LinearGradientOp{Stop1: f32.Pt(0, float32(y0)), Color1: c0, Stop2: f32.Pt(0, float32(y1)), Color2: c1}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		stack.Pop()
	}
}
