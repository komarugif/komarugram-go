// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// shadowLayers is how many outlines a shadow is built of: each one a little
// larger and as faint, so that together they fade out like a blur.
const shadowLayers = 20

// darkShadowBoost makes shadows darker on dark themes.
const darkShadowBoost = 1.7

// shadowStyle is the shadow of a dialog or a menu, which sets it off from
// the scrim or the content under it.
type shadowStyle struct {
	// blur is how far the shadow reaches beyond the shape, dy how far it
	// is shifted down, as if the light were above.
	blur, dy unit.Dp
	// alpha is how dark it is next to the shape, from 0 to 1.
	alpha float32
}

var (
	// dialogShadow is the shadow of a dialog over its scrim.
	dialogShadow = shadowStyle{blur: 28, dy: 8, alpha: .42}
	// menuShadow is the shadow of a context menu or a picker over the page.
	menuShadow = shadowStyle{blur: 18, dy: 5, alpha: .36}
)

// drawShadow draws the shadow of a rounded rectangle, under what is drawn
// next. It leaves out the shape itself, where the content will be.
func drawShadow(gtx layout.Context, rect image.Rectangle, radius int, style shadowStyle) {
	if rect.Empty() {
		return
	}
	blur := float32(gtx.Dp(style.blur))
	dy := gtx.Dp(style.dy)
	// Each layer adds up with the ones larger than it, so that the shadow is
	// darkest at the shape and falls off to the edge of the blur.
	sc := scheme(gtx)
	alpha := style.alpha
	if surface := sc.Surface.Color; int(surface.R)+int(surface.G)+int(surface.B) < 3*128 {
		// Black on a dark surface needs more to be seen.
		alpha = min(alpha*darkShadowBoost, .9)
	}
	color := sc.Shadow.SetOpacity(token.OpacityLevel(alpha / shadowLayers)).AsNRGBA()
	for i := shadowLayers; i > 0; i-- {
		// The squared spacing puts more layers near the shape, where the
		// fall-off is steepest.
		t := float32(i) / shadowLayers
		spread := int(blur*t*t + .5)
		outline := rect.Add(image.Pt(0, dy)).Inset(-spread)
		paint.FillShape(gtx.Ops, color, clip.UniformRRect(outline, radius+spread).Op(gtx.Ops))
	}
}
