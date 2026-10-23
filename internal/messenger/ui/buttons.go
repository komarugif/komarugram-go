// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strconv"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
)

// textButton draws a flat button of txt, which a surface makes react.
func textButton(gtx layout.Context, s *surface, txt string) layout.Dimensions {
	return textButtonColor(gtx, s, txt, scheme(gtx).Primary.Color)
}

func textButtonColor(gtx layout.Context, s *surface, txt string, col token.MatColor) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 7, Bottom: 7, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return label(gtx, txt, token.TypestyleLabelLarge, col, 1)
	})
	call := macro.Stop()
	style := surfaceStyle{radius: gtx.Dp(8), background: col.SetOpacity(0), content: col, button: txt}
	s.Layout(gtx, dims.Size, style, func(gtx layout.Context) layout.Dimensions {
		call.Add(gtx.Ops)
		return dims
	})
	return dims
}

// tonalButton draws a tonal button of txt followed by count, as the actions
// over selected messages have; a count of 0 is left out.
func tonalButton(gtx layout.Context, s *surface, txt string, count int) layout.Dimensions {
	sc := scheme(gtx)
	col := sc.SecondaryContainer.OnColor
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 8, Bottom: 8, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, txt, token.TypestyleLabelLarge, col, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if count == 0 {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// The count is in the label's color: faded, it read at 2.7:1 on
					// the light container.
					return label(gtx, strconv.Itoa(count), token.TypestyleLabelLargeEmphasized, col, 1)
				})
			}),
		)
	})
	call := macro.Stop()
	style := surfaceStyle{radius: gtx.Dp(8), background: sc.SecondaryContainer.Color, content: col, button: txt}
	s.Layout(gtx, dims.Size, style, func(gtx layout.Context) layout.Dimensions {
		call.Add(gtx.Ops)
		return dims
	})
	return dims
}

// Full-size icons and a 48 dp target match the application's navigation controls.
func navigationButton(gtx layout.Context, s *surface, icon wdk.IconWidget, title string) layout.Dimensions {
	return navigationButtonColor(gtx, s, icon, title, scheme(gtx).Surface.OnColor)
}

// navigationButtonColor is navigationButton in content, over a background
// of another color than the surface's.
func navigationButtonColor(gtx layout.Context, s *surface, icon wdk.IconWidget, title string, content token.MatColor) layout.Dimensions {
	size := image.Pt(gtx.Dp(48), gtx.Dp(48))
	gtx.Constraints = layout.Exact(size)
	style := surfaceStyle{radius: size.Y / 2, background: content.SetOpacity(0), content: content, button: title}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(24), gtx.Dp(24)))
			return icon(gtx, content)
		})
	})
}
