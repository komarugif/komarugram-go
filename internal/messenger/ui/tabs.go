// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

const (
	// tabSwitchDuration is how long switching tabs animates.
	tabSwitchDuration = token.DurationMedium2
	// tabSlideDistance is how far the content of a tab slides in, as a
	// part of its width.
	tabSlideDistance = 0.25
)

// tabRow is a row of tabs of equal width, as the composer's picker and the
// search have: each title centered in its tab, the active one's in the
// primary color, an indicator under it that slides to the tab switched to,
// and a state layer and ripple on each tab. The content of a tab switched
// to slides in from its side: see Slide. The caller keeps which tab is
// active.
type tabRow struct {
	disabled  func(int) bool
	tabs      []surface
	colors    []wdk.ColorTween
	indicator wdk.FloatTween
	slide     wdk.FloatTween
	// from is the side the content switched to comes from, -1 or 1;
	// switched starts its slide on the next frame.
	from     int
	switched bool
}

func (r *tabRow) grow(n int) {
	for len(r.tabs) < n {
		r.tabs = append(r.tabs, surface{})
		r.colors = append(r.colors, wdk.ColorTween{})
	}
}

// Clicked returns the tab clicked, if it is not active, and switches to it.
func (r *tabRow) Clicked(gtx layout.Context, active, n int) (int, bool) {
	r.grow(n)
	for i := range n {
		if r.tabs[i].Clicked(gtx) && i != active && (r.disabled == nil || !r.disabled(i)) {
			r.Switch(active, i)
			return i, true
		}
	}
	return active, false
}

// Switch animates switching from one tab to another, as a click does.
func (r *tabRow) Switch(from, to int) {
	r.from = 1
	if to < from {
		r.from = -1
	}
	r.switched = true
}

// Layout draws the tabs titled labels in the exact size of the constraints.
func (r *tabRow) Layout(gtx layout.Context, labels []string, active int) layout.Dimensions {
	return r.LayoutWith(gtx, len(labels), active, func(gtx layout.Context, i int, col token.MatColor) layout.Dimensions {
		return label(gtx, labels[i], token.TypestyleLabelLarge, col, 1)
	})
}

// LayoutWith draws n tabs as Layout does, their titles drawn by title, in
// col, for a title a label cannot draw, as a custom emoji.
func (r *tabRow) LayoutWith(gtx layout.Context, n, active int, title func(gtx layout.Context, i int, col token.MatColor) layout.Dimensions) layout.Dimensions {
	r.grow(n)
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	if n == 0 {
		return layout.Dimensions{Size: size}
	}
	for i := range n {
		col := sc.SurfaceVariant.OnColor
		if i == active {
			col = sc.Primary.Color
		}
		r.colors[i].Duration = tabSwitchDuration
		col = r.colors[i].Animate(gtx, col)
		area := image.Rect(i*size.X/n, 0, (i+1)*size.X/n, size.Y)
		inRect(gtx, area, func(gtx layout.Context) layout.Dimensions {
			tab := gtx.Constraints.Max
			if r.disabled != nil && r.disabled(i) {
				gtx = gtx.Disabled()
				col = col.SetOpacity(0.38)
			}
			style := surfaceStyle{background: col.SetOpacity(0), content: col}
			return r.tabs[i].Layout(gtx, tab, style, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(tab)
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Point{}
					return title(gtx, i, col)
				})
			})
		})
	}
	// The indicator moves to the tab switched to.
	r.indicator.Duration = tabSwitchDuration
	r.indicator.Easing = &token.EasingStandard
	at := r.indicator.Animate(gtx, float32(active))
	width := float32(size.X) / float32(n)
	indicator := image.Rect(int(at*width)+gtx.Dp(16), max(0, size.Y-gtx.Dp(3)), int((at+1)*width)-gtx.Dp(16), size.Y)
	inRect(gtx, indicator, func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, sc.Primary.Color, gtx.Constraints.Max, gtx.Dp(2))
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	return layout.Dimensions{Size: size}
}

// Slide draws w, the content of the active tab, in area: after a switch it
// slides in from the side of the tab switched from and fades in.
func (r *tabRow) Slide(gtx layout.Context, area image.Rectangle, w layout.Widget) {
	if r.switched {
		r.slide = wdk.FloatTween{}
		r.slide.Animate(gtx, 0)
		r.switched = false
	}
	r.slide.Duration = tabSwitchDuration
	r.slide.Easing = &token.EasingEmphasizedDecelerate
	slide := r.slide.Animate(gtx, 1)
	if slide == 1 {
		inRect(gtx, area, w)
		return
	}
	defer clip.Rect(area).Push(gtx.Ops).Pop()
	dx := int(float32(r.from*area.Dx()) * tabSlideDistance * (1 - slide))
	defer paint.PushOpacity(gtx.Ops, slide).Pop()
	inRect(gtx, area.Add(image.Pt(dx, 0)), w)
}
