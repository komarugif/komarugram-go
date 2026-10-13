// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"strings"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
)

const (
	cornerButton = unit.Dp(44)
	cornerGap    = unit.Dp(4)
	// cornerMargin is the room above and below the sign-in's card, in which
	// the controls of the corner are: the card never reaches them.
	cornerMargin = unit.Dp(56)
)

// loginCorner is what the sign-in offers in the corner of the window before
// there is an account to have settings: the language, the theme and the proxy,
// without which a window that cannot reach Telegram could not be read, or
// gone on from.
type loginCorner struct {
	mode        func() themeMode
	setMode     func(themeMode)
	language    func() string
	setLanguage func(string)
	proxy       *proxyDialog

	themeButton, languageButton, proxyButton surface
}

// nextTheme is the theme the button of the theme changes to.
func nextTheme(m themeMode) themeMode {
	switch m {
	case themeAuto:
		return themeLight
	case themeLight:
		return themeDark
	}
	return themeAuto
}

// nextLanguage is the language that follows current in the list of the ones
// the client comes with.
func nextLanguage(current string, codes []string) string {
	if len(codes) == 0 {
		return current
	}
	i := slices.Index(codes, current)
	return codes[(i+1)%len(codes)]
}

// Update handles the buttons.
func (c *loginCorner) Update(gtx layout.Context) {
	if c.themeButton.Clicked(gtx) {
		c.setMode(nextTheme(c.mode()))
	}
	if c.languageButton.Clicked(gtx) {
		c.setLanguage(nextLanguage(c.language(), languageCodes()))
	}
	if c.proxyButton.Clicked(gtx) {
		c.proxy.Open(gtx)
	}
}

// Layout draws the buttons in the top right corner of the window.
func (c *loginCorner) Layout(gtx layout.Context, l localization.Catalog) {
	sc := scheme(gtx)
	size := gtx.Dp(cornerButton)
	box := image.Pt(size, size)
	style := func(name string) surfaceStyle {
		return surfaceStyle{radius: size / 2, background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: name}
	}
	icon := func(gtx layout.Context, icon wdk.IconWidget) layout.Dimensions {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(24), gtx.Dp(24)))
			return icon(gtx, sc.Surface.OnColor)
		})
	}
	themeIcon := iconAutoTheme
	switch c.mode() {
	case themeLight:
		themeIcon = iconLight
	case themeDark:
		themeIcon = iconDark
	}
	_, proxied := c.proxy.view.store.Active()

	buttons := []func(gtx layout.Context) layout.Dimensions{
		func(gtx layout.Context) layout.Dimensions {
			return c.languageButton.Layout(gtx, box, style(l.T("login.language")), func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(box)
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Point{}
					return label(gtx, strings.ToUpper(c.language()), token.TypestyleLabelLarge, sc.Surface.OnColor, 1)
				})
			})
		},
		func(gtx layout.Context) layout.Dimensions {
			return c.themeButton.Layout(gtx, box, style(l.T("login.theme")), func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(box)
				return icon(gtx, themeIcon)
			})
		},
		func(gtx layout.Context) layout.Dimensions {
			dims := c.proxyButton.Layout(gtx, box, style(l.T("login.proxy")), func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(box)
				return icon(gtx, iconProxy)
			})
			if proxied {
				// A proxy is in use: a dot at the corner of the icon says so.
				dot := gtx.Dp(8)
				at := image.Pt(size/2+gtx.Dp(6), size/2-gtx.Dp(12))
				paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse(image.Rectangle{Min: at, Max: at.Add(image.Pt(dot, dot))}).Op(gtx.Ops))
			}
			return dims
		},
	}
	gap := gtx.Dp(cornerGap)
	width := len(buttons)*size + (len(buttons)-1)*gap
	x := gtx.Constraints.Max.X - width - gtx.Dp(12)
	for i, button := range buttons {
		offset(gtx, image.Pt(x+i*(size+gap), gtx.Dp(8)), button)
	}
}

// proxyDialog is the proxies in a dialog, for the sign-in, which has no
// settings to hold them.
type proxyDialog struct {
	modal modal
	view  *proxyView
	list  scroll.List
	done  surface
}

func newProxyDialog(view *proxyView) *proxyDialog {
	return &proxyDialog{view: view, list: scroll.List{List: layout.List{Axis: layout.Vertical}}}
}

// Open shows the dialog, and measures the proxies again.
func (d *proxyDialog) Open(gtx layout.Context) {
	d.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
	d.view.Open()
	d.modal.Open()
	// The field takes the focus, not the dialog: as the country picker does it.
	d.modal.focus = false
	d.view.link.Focus(gtx)
}

// Layout draws the dialog while it is open.
func (d *proxyDialog) Layout(gtx layout.Context, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	if !d.modal.closing {
		d.view.Update(gtx)
		if d.done.Clicked(gtx) {
			d.modal.Close()
		}
	}
	sc := scheme(gtx)
	d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(480))
		gtx.Constraints.Max.Y = max(gtx.Constraints.Max.Y-gtx.Dp(48), 0)
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{X: gtx.Constraints.Max.X}
			// The title, the field and the buttons stay in sight, and the list
			// between them scrolls where it is too long for the window.
			part := func(w layout.Widget) (op.CallOp, image.Point) {
				gtx := gtx
				gtx.Constraints.Min = image.Point{}
				macro := op.Record(gtx.Ops)
				dims := w(gtx)
				return macro.Stop(), dims.Size
			}
			titleCall, titleSize := part(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("proxy.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
			})
			formCall, formSize := part(func(gtx layout.Context) layout.Dimensions { return d.view.LayoutForm(gtx, l) })
			checkCall, checkSize := part(func(gtx layout.Context) layout.Dimensions { return d.view.LayoutCheck(gtx, l) })
			doneCall, doneSize := part(func(gtx layout.Context) layout.Dimensions {
				return textButton(gtx, &d.done, l.T("proxy.close"))
			})
			gap := gtx.Dp(12)
			buttons := max(checkSize.Y, doneSize.Y)
			room := max(gtx.Constraints.Max.Y-titleSize.Y-formSize.Y-buttons-3*gap, 0)
			// The outline of a row is drawn on its edge, half of it outside:
			// the list reaches past its content by as much, so that its clip
			// does not cut the outlines.
			edge := gtx.Dp(protectEdge)
			list := gtx
			list.Constraints.Max.X += 2 * edge
			list.Constraints.Min = image.Pt(list.Constraints.Max.X, 0)
			list.Constraints.Max.Y = room
			macro := op.Record(gtx.Ops)
			listDims := d.list.Layout(list, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				return layout.Inset{Left: protectEdge, Right: protectEdge}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return d.view.LayoutList(gtx, l)
				})
			})
			listCall := macro.Stop()

			place := func(x, y int, call op.CallOp, size image.Point) {
				offset(gtx, image.Pt(x, y), func(gtx layout.Context) layout.Dimensions {
					call.Add(gtx.Ops)
					return layout.Dimensions{Size: size}
				})
			}
			y := 0
			place(0, y, titleCall, titleSize)
			y += titleSize.Y + gap
			place(0, y, formCall, formSize)
			y += formSize.Y + gap
			place(-edge, y, listCall, listDims.Size)
			y += listDims.Size.Y + gap
			place(0, y+(buttons-checkSize.Y)/2, checkCall, checkSize)
			place(gtx.Constraints.Max.X-doneSize.X, y+(buttons-doneSize.Y)/2, doneCall, doneSize)
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, y+buttons)}
		}, defaultCardPadding)
	})
}
