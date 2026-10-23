// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"image"
	"math"
	"strconv"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"rsc.io/qr"

	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/f32"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/assets"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/pkg/resample"
)

// The QR code of the sign-in, in Telegram Desktop's measures
// (intro.style): the code at most qrMaxSize wide on a plate qrPlate wider
// on each side, with the logo in the middle.
const (
	qrMaxSize = unit.Dp(180)
	// qrMinSize is the least the code shrinks to in a window too low for
	// the whole card.
	qrMinSize     = unit.Dp(120)
	qrPlate       = unit.Dp(12)
	qrPlateRadius = unit.Dp(8)
	// qrLoadingSize is the ring that stands in for the code.
	qrLoadingSize = unit.Dp(44)
	// qrLogoSize is the logo, as wide as Telegram Desktop's round one.
	qrLogoSize = unit.Dp(44)
	// qrLogoGap is the plate around the logo, between it and the modules.
	qrLogoGap = unit.Dp(2)
	// qrInvertGap is how far the button that inverts the code is from the
	// plate.
	qrInvertGap = unit.Dp(8)
)

// qrView is the step of the sign-in that shows a QR code to scan with a
// device where the account is signed in, as Telegram Desktop shows it
// (intro_qr.cpp): the code, the title, the three steps of scanning it, and
// the way to the phone number. Before the code has its token, the plate
// has a loading ring in its middle.
//
// In a dark theme the code is light on a dark plate, which Telegram's apps
// read; a button beside it makes it dark on white, for the scanners that
// read only that.
type qrView struct {
	usePhone *button.Button
	invert   surface
	// inverted is set when the code is dark on white in a dark theme.
	inverted bool
	loading  loadingIndicator

	// link is the text of code, encoded when it changes.
	link string
	code *qr.Code

	// logo is logo.svg, drawn as logoImage logoPx wide.
	logo      *oksvg.SvgIcon
	logoImage paint.ImageOp
	logoPx    int
}

func newQRView() *qrView {
	return &qrView{usePhone: button.Text()}
}

// Update hands the way to the phone number to l, and inverts the code.
func (v *qrView) Update(gtx layout.Context, l *login.Login) {
	if v.usePhone.Clicked(gtx) {
		l.UsePhone()
	}
	if v.invert.Clicked(gtx) && isDark(gtx) {
		v.inverted = !v.inverted
	}
}

func (v *qrView) Layout(gtx layout.Context, st login.State, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	centered := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.N.Layout(gtx, w)
		})
	}
	rows := []layout.FlexChild{
		// The code takes the room the rest leaves it, up to its size.
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, 0)
			return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				dims := v.layoutCode(gtx, st.QRLink, l)
				if isDark(gtx) {
					offset(gtx, image.Pt(dims.Size.X+gtx.Dp(qrInvertGap), 0), func(gtx layout.Context) layout.Dimensions {
						return v.layoutInvert(gtx, l)
					})
				}
				return dims
			})
		}),
		vspace(20),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return centeredLabel(gtx, l.T("login.title_qr"), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 0)
		}),
		vspace(8),
	}
	for i, key := range []string{"login.qr_step1", "login.qr_step2", "login.qr_step3"} {
		rows = append(rows, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return qrStep(gtx, i+1, l.T(key))
		}))
	}
	rows = append(rows, vspace(20), centered(func(gtx layout.Context) layout.Dimensions {
		return v.usePhone.Layout(gtx, l.T("login.use_phone"))
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// layoutInvert draws the button that inverts the code, selected while the
// code is dark on white.
func (v *qrView) layoutInvert(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Dp(cornerButton)
	style := surfaceStyle{radius: size / 2, background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: l.T("login.qr_invert")}
	if v.inverted {
		style.background, style.content = sc.SecondaryContainer.Color, sc.SecondaryContainer.OnColor
	}
	return v.invert.Layout(gtx, image.Pt(size, size), style, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(size, size))
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(24), gtx.Dp(24)))
			return iconInvertColors(gtx, style.content)
		})
	})
}

// qrStep is one of the numbered steps of scanning the code: the number
// before the text, and the lines of the text under one another.
func qrStep(gtx layout.Context, n int, text string) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(20)
			return label(gtx, strconv.Itoa(n)+".", token.TypestyleTitleSmall, sc.Surface.OnColor, 1)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
	)
}

// qrColors are the plate and the modules: black on white in a light theme
// or inverted, the text's color on a container's in a dark one.
func (v *qrView) qrColors(gtx layout.Context) (plate, modules token.MatColor) {
	if !isDark(gtx) || v.inverted {
		return white, token.NewMatColorFromHexRGB(0x000000)
	}
	sc := scheme(gtx)
	return sc.SurfaceContainerHighest, sc.Surface.OnColor
}

// layoutCode draws the code of link on its plate; "" is the plate with a
// loading ring.
func (v *qrView) layoutCode(gtx layout.Context, link string, l localization.Catalog) layout.Dimensions {
	if link != v.link {
		v.link = link
		v.code = nil
		if link != "" {
			// Quartile, as Telegram Desktop: a quarter of the code may be
			// lost, and the logo covers some of it.
			v.code, _ = qr.Encode(link, qr.Q)
		}
	}
	pad := gtx.Dp(qrPlate)
	maxSize := min(gtx.Dp(qrMaxSize), max(gtx.Constraints.Max.Y-2*pad, gtx.Dp(qrMinSize)))
	// The plate keeps its size as the code comes, for the card not to jump.
	plate := maxSize + 2*pad
	plateColor, moduleColor := v.qrColors(gtx)
	semantic.LabelOp(l.T("login.title_qr")).Add(gtx.Ops)
	fillRounded(gtx, plateColor, image.Pt(plate, plate), gtx.Dp(qrPlateRadius))
	if v.code == nil {
		ring := gtx.Dp(qrLoadingSize)
		offset(gtx, image.Pt((plate-ring)/2, (plate-ring)/2), func(gtx layout.Context) layout.Dimensions {
			return v.loading.sized(gtx, l, qrLoadingSize)
		})
		return layout.Dimensions{Size: image.Pt(plate, plate)}
	}
	// Whole pixels a module, for a sharp code.
	modulePx := max(maxSize/v.code.Size, 1)
	size := modulePx * v.code.Size
	corner := (plate - size) / 2
	logo, gap := qrLogoPx(gtx, size)
	hole := qrHole(v.code.Size, modulePx, logo+2*gap)
	drawQR(gtx, v.code, image.Pt(corner, corner), modulePx, hole, moduleColor)
	// The logo fills the hole but for the gap, square as logo.svg is.
	holePx := hole.Dx() * modulePx
	logoPx := holePx - 2*gap
	at := image.Pt(corner+hole.Min.X*modulePx+(holePx-logoPx)/2, corner+hole.Min.Y*modulePx+(holePx-logoPx)/2)
	if img, ok := v.logoAt(logoPx); ok {
		offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
			img.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			return layout.Dimensions{}
		})
	}
	return layout.Dimensions{Size: image.Pt(plate, plate)}
}

// qrLogoPx is the logo and the gap around it for a code size pixels wide:
// they keep their share of the code as it shrinks, for the logo not to
// cover more of a small code than of a large one.
func qrLogoPx(gtx layout.Context, size int) (logo, gap int) {
	full := gtx.Dp(qrMaxSize)
	if size >= full {
		return gtx.Dp(qrLogoSize), gtx.Dp(qrLogoGap)
	}
	return max(1, gtx.Dp(qrLogoSize)*size/full), max(1, gtx.Dp(qrLogoGap)*size/full)
}

// qrHole is the square of modules, in the middle of a code size modules
// wide, that the logo covers: the fewest that hold px pixels, odd or even
// as size is, to be in the middle of the code.
func qrHole(size, modulePx, px int) image.Rectangle {
	n := (px + modulePx - 1) / modulePx
	if n%2 != size%2 {
		n++
	}
	n = min(n, size)
	start := (size - n) / 2
	return image.Rect(start, start, start+n, start+n)
}

// logoAt returns logo.svg drawn px wide, drawn again only when the size
// changes, so that it is as sharp as the screen.
func (v *qrView) logoAt(px int) (paint.ImageOp, bool) {
	if px <= 0 {
		return paint.ImageOp{}, false
	}
	if px == v.logoPx {
		return v.logoImage, true
	}
	if v.logo == nil {
		icon, err := oksvg.ReadIconStream(bytes.NewReader(assets.Logo))
		if err != nil {
			return paint.ImageOp{}, false
		}
		v.logo = icon
	}
	v.logoPx, v.logoImage = px, paint.NewImageOp(rasterizeLogo(v.logo, px))
	return v.logoImage, true
}

// rasterizeLogo draws icon px wide and tall. oksvg draws a stroke as wide
// as the SVG's units say whatever the scale, and a gradient in the SVG's
// units in pixels: the icon is drawn at its own size, one pixel a unit,
// where both are right, and scaled from there.
func rasterizeLogo(icon *oksvg.SvgIcon, px int) *image.RGBA {
	native := max(int(math.Round(icon.ViewBox.W)), 1)
	src := image.NewRGBA(image.Rect(0, 0, native, native))
	icon.SetTarget(0, 0, float64(native), float64(native))
	icon.Draw(rasterx.NewDasher(native, native, rasterx.NewScannerGV(native, native, src, src.Bounds())), 1)
	if px == native {
		return src
	}
	return resample.Resize(src, px, px)
}

// drawQR fills the dark modules of code in c, px pixels each, from origin,
// but those in hole, where the logo goes: one shape of the runs of them in
// each row.
func drawQR(gtx layout.Context, code *qr.Code, origin image.Point, px int, hole image.Rectangle, c token.MatColor) {
	var path clip.Path
	path.Begin(gtx.Ops)
	rect := func(x0, y0, x1, y1 int) {
		path.MoveTo(f32.Pt(float32(origin.X+x0), float32(origin.Y+y0)))
		path.LineTo(f32.Pt(float32(origin.X+x1), float32(origin.Y+y0)))
		path.LineTo(f32.Pt(float32(origin.X+x1), float32(origin.Y+y1)))
		path.LineTo(f32.Pt(float32(origin.X+x0), float32(origin.Y+y1)))
		path.Close()
	}
	dark := func(x, y int) bool {
		return code.Black(x, y) && !image.Pt(x, y).In(hole)
	}
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; {
			if !dark(x, y) {
				x++
				continue
			}
			start := x
			for x < code.Size && dark(x, y) {
				x++
			}
			rect(start*px, y*px, x*px, (y+1)*px)
		}
	}
	paint.FillShape(gtx.Ops, c.AsNRGBA(), clip.Outline{Path: path.End()}.Op())
}
