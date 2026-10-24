// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"komarugram/internal/messenger/localization"

	"gio-mw/widget/indicator"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
)

type loadingIndicator struct{ circle *indicator.Indicator }

func (s *loadingIndicator) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	if s.circle == nil {
		s.circle = indicator.CircularIndeterminate()
	}
	semantic.LabelOp(l.T("history.loading")).Add(gtx.Ops)
	return s.circle.Layout(gtx)
}

// sized draws the ring d wide and tall, in an area of its own: what tells
// it, as a screen reader, finds it where it is drawn.
func (s *loadingIndicator) sized(gtx layout.Context, l localization.Catalog, d unit.Dp) layout.Dimensions {
	n := gtx.Dp(d)
	gtx.Constraints = layout.Exact(image.Pt(n, n))
	defer clip.Rect{Max: image.Pt(n, n)}.Push(gtx.Ops).Pop()
	return s.Layout(gtx, l)
}

// centered draws the ring d wide in the middle of the constraints' width.
func (s *loadingIndicator) centered(gtx layout.Context, l localization.Catalog, d unit.Dp) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return s.sized(gtx, l, d)
	})
}

// page occupies the content viewport, keeping navigation outside it usable.
func (s *loadingIndicator) page(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	size := gtx.Constraints.Max
	fillRect(gtx, scheme(gtx).SurfaceContainerLow, size)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		n := min(gtx.Dp(unit.Dp(48)), min(size.X, size.Y))
		gtx.Constraints = layout.Exact(image.Pt(n, n))
		return s.Layout(gtx, l)
	})
	return layout.Dimensions{Size: size}
}
