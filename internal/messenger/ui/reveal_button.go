// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

const (
	revealButtonHeight unit.Dp = 40
	revealButtonIcon   unit.Dp = 24
	revealButtonPad    unit.Dp = 8
	revealButtonGap    unit.Dp = 8
)

// revealButton is an icon whose text shows beside it, in the same line,
// while the pointer is on it: the button widens to the text and the text
// fades in, as the settings of motion allow.
type revealButton struct {
	surface surface
	open    wdk.FloatTween
}

// Clicked reports whether the button was clicked since the last call.
func (b *revealButton) Clicked(gtx layout.Context) bool {
	return b.surface.Clicked(gtx)
}

// Layout draws the button of icon, in col, with text revealed on hover.
func (b *revealButton) Layout(gtx layout.Context, icon wdk.IconWidget, text string, col token.MatColor) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	textDims := label(gtx, text, token.TypestyleLabelLarge, col, 1)
	textCall := macro.Stop()

	target := float32(0)
	if b.surface.click.Hovered() || gtx.Focused(&b.surface.click) {
		target = 1
	}
	b.open.Duration, b.open.Easing = token.DurationMedium2, &token.EasingEmphasizedDecelerate
	t := b.open.Animate(gtx, target)

	pad, iconSize, gap := gtx.Dp(revealButtonPad), gtx.Dp(revealButtonIcon), gtx.Dp(revealButtonGap)
	height := gtx.Dp(revealButtonHeight)
	reveal := int(t * float32(gap+textDims.Size.X+pad))
	size := image.Pt(pad+iconSize+pad+reveal, height)
	style := surfaceStyle{radius: height / 2, background: col.SetOpacity(0), content: col, button: text}
	return b.surface.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		offset(gtx, image.Pt(pad, (height-iconSize)/2), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(iconSize, iconSize))
			return icon(gtx, col)
		})
		if reveal > 0 {
			// The text is cut where the button ends, and fades in as it
			// widens.
			x := pad + iconSize + gap
			defer clip.Rect{Min: image.Pt(x, 0), Max: image.Pt(size.X-pad/2, height)}.Push(gtx.Ops).Pop()
			defer paint.PushOpacity(gtx.Ops, t).Pop()
			offset(gtx, image.Pt(x, (height-textDims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
				textCall.Add(gtx.Ops)
				return textDims
			})
		}
		return layout.Dimensions{Size: size}
	})
}
