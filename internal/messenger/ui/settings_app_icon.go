// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/appicon"
	"komarugram/internal/messenger/localization"
)

// appIconSettings is the card in Settings -> Appearance that lets the user
// choose between the bundled application icons (default, New Year).
type appIconSettings struct {
	icon    func() string
	setIcon func(string)
	buttons []surface
	cachePx int
	cache   map[string]paint.ImageOp
}

func newAppIconSettings() *appIconSettings {
	return &appIconSettings{
		buttons: make([]surface, len(appicon.All)),
		cache:   make(map[string]paint.ImageOp, len(appicon.All)),
	}
}

func (a *appIconSettings) shown() bool {
	return a != nil && a.icon != nil && a.setIcon != nil
}

func (a *appIconSettings) Update(gtx layout.Context) {
	if !a.shown() {
		return
	}
	for i, name := range appicon.All {
		if a.buttons[i].Clicked(gtx) {
			a.setIcon(name)
		}
	}
}

func (a *appIconSettings) imageOp(name string, px int) paint.ImageOp {
	if px <= 0 {
		px = 64
	}
	if a.cachePx != px {
		clear(a.cache)
		a.cachePx = px
	}
	if op, ok := a.cache[name]; ok {
		return op
	}
	op := paint.NewImageOp(appicon.ImageFor(name, px))
	op.Filter = paint.FilterLinear
	a.cache[name] = op
	return op
}

func appIconLabel(name string, l localization.Catalog) string {
	if name == appicon.NewYear {
		return l.T("settings.app_icon_newyear")
	}
	return l.T("settings.app_icon_default")
}

func (a *appIconSettings) Layout(gtx layout.Context, height *heightTransition, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	current := appicon.Resolve(a.icon(), time.Now())
	return height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("settings.app_icon"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
			}),
			vspace(12),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				tile := gtx.Dp(80)
				iconPx := gtx.Dp(60)
				children := make([]layout.FlexChild, 0, len(appicon.All)*2)
				for i, name := range appicon.All {
					if i > 0 {
						children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout))
					}
					idx, iconName := i, name
					selected := current == iconName
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						bg := sc.SurfaceContainerHigh.SetOpacity(0)
						if selected {
							bg = sc.SecondaryContainer.Color
						}
						style := surfaceStyle{
							radius:     gtx.Dp(16),
							background: bg,
							content:    sc.Surface.OnColor,
							button:     appIconLabel(iconName, l),
						}
						return a.buttons[idx].Layout(gtx, image.Pt(tile, tile), style, func(gtx layout.Context) layout.Dimensions {
							pad := (tile - iconPx) / 2
							offset(gtx, image.Pt(pad, pad), func(gtx layout.Context) layout.Dimensions {
								defer clip.Ellipse{Max: image.Pt(iconPx, iconPx)}.Push(gtx.Ops).Pop()
								img := widget.Image{Src: a.imageOp(iconName, iconPx), Fit: widget.Cover}
								cgtx := gtx
								cgtx.Constraints = layout.Exact(image.Pt(iconPx, iconPx))
								return img.Layout(cgtx)
							})
							return layout.Dimensions{Size: image.Pt(tile, tile)}
						})
					}))
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
			}),
		)
	}, defaultCardPadding)
}
