// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"strconv"
	"strings"

	"rsc.io/qr"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/button"
	"gio-mw/widget/scroll"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chattheme"
	"komarugram/internal/messenger/localization"
)

// The QR code of a profile, in the measures of its page.
const (
	// qrCardWidth is the white card of the code at its widest.
	qrCardWidth  = unit.Dp(300)
	qrCardPad    = unit.Dp(24)
	qrCardRadius = unit.Dp(20)
	// qrCardAvatar is the avatar that sits on the card's top edge, and
	// qrAvatarRing the card's white around it.
	qrCardAvatar = unit.Dp(64)
	qrAvatarRing = unit.Dp(4)
	// qrSwatchSize is the least a background's swatch is wide: they fill
	// their row.
	qrSwatchSize   = unit.Dp(90)
	qrSwatchGap    = unit.Dp(12)
	qrSwatchRadius = unit.Dp(14)
	// qrShotMargin is the background around the card in the picture copied.
	qrShotMargin = unit.Dp(40)
	// qrGradientSide is the side of a background's picture, which is drawn
	// stretched: a gradient has nothing sharp to lose.
	qrGradientSide = 96
)

// qrGradients are the backgrounds a profile's code is offered in, four
// colors of a freeform gradient each (chattheme.Gradient), as Telegram's
// wallpapers are. The code is drawn in them on white, so none is light:
// a scanner tells the modules from the card by how bright they are.
var qrGradients = [][]uint32{
	{0x5B9EF0, 0xDD5A9E, 0x9B6BF2, 0x5FA2F0},
	{0x4A9BE6, 0x35B3A2, 0x5A8CE6, 0x3BBBAA},
	{0x8C79E8, 0x5A99EE, 0xD667B2, 0xA277EA},
	{0x3CB377, 0x2DA29C, 0x4A8CDD, 0x7FAE2F},
	{0xEE8658, 0xE55D89, 0xB567DD, 0xE39A2B},
	{0xAD6BE5, 0x8869EE, 0xE266A5, 0xEE8866},
	{0xE39A2B, 0xE67038, 0xDD5197, 0xE8873A},
	{0xD65BCD, 0xE55687, 0x7A69EE, 0xB15BE5},
	{0x2CA5C4, 0x3D7BDD, 0x685CDD, 0x3CB0AB},
	{0xE25048, 0xE56C47, 0xD6448C, 0xEA8A42},
	{0x5C9A3C, 0x3C9B6C, 0x2D8B9C, 0x8FA537},
	{0x4A5568, 0x2D3748, 0x5A4FCF, 0x3F6FB0},
}

// qrPage is the page of a chat's QR code in its info, as Telegram
// Desktop's box of a profile's code was (peer_qr_box.cpp) before it took
// Telegram's themes: the code of the chat's link in the colors of a
// gradient, on a white card under the avatar, with the username below,
// the gradients to choose from, and a button that copies the card on its
// gradient as a picture. It is there for a chat that has a username.
type qrPage struct {
	shown bool
	// open is the button in the info's header, opposite the one that
	// closes or edits it.
	open surface
	copy *button.Button
	// swatches are the backgrounds offered, and choice the one chosen,
	// which the next chat's code keeps.
	swatches []surface
	choice   int
	list     scroll.List

	// link is the text of code, encoded when it changes.
	link string
	code *qr.Code
	// gradients are the pictures of the backgrounds, made as they are drawn.
	gradients []*image.RGBA
	// results brings the picture being copied.
	results chan qrShot
}

// qrShot is the card as a PNG, or why there is none.
type qrShot struct {
	png []byte
	err error
}

// gradient is the picture of background i.
func (q *qrPage) gradient(i int) *image.RGBA {
	if len(q.gradients) != len(qrGradients) {
		q.gradients = make([]*image.RGBA, len(qrGradients))
	}
	if q.gradients[i] == nil {
		q.gradients[i] = chattheme.Gradient(qrGradients[i], qrGradientSide, qrGradientSide, 0, 0)
	}
	return q.gradients[i]
}

// reset closes the page and drops its pictures; the choice stays.
func (q *qrPage) reset() {
	*q = qrPage{copy: q.copy, choice: q.choice}
}

// qrLink is the link the code of the chat shown opens; "" for a chat
// without a username, which has no code.
func (p *chatInfo) qrLink() string {
	name := p.chatUsername()
	if name == "" {
		return ""
	}
	return "https://t.me/" + name
}

// OpenQR shows the page of the chat's QR code.
func (p *chatInfo) OpenQR() {
	if p.qrLink() == "" {
		return
	}
	p.stop()
	p.qr.shown = true
	p.qr.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
}

// updateQR takes the clicks of the code's button and page, and the
// picture that was asked for, which goes to the clipboard.
func (p *chatInfo) updateQR(gtx layout.Context, l localization.Catalog) {
	q := &p.qr
	select {
	case r := <-q.results:
		q.results = nil
		if r.err != nil {
			p.modal.Toast(l.T("history.snapshot_failed") + ": " + mediaErrorText(r.err))
			break
		}
		gtx.Execute(clipboard.WriteCmd{Type: "image/png", Data: io.NopCloser(bytes.NewReader(r.png))})
		p.modal.Toast(l.T("info.qr_copied"))
	default:
	}
	if q.open.Clicked(gtx) && !q.shown {
		p.OpenQR()
	}
	if !q.shown {
		return
	}
	for i := range q.swatches {
		if q.swatches[i].Clicked(gtx) {
			q.choice = i
		}
	}
	if q.copy.Clicked(gtx) && q.results == nil {
		p.copyQR(gtx)
	}
}

// copyQR draws the card on its gradient off the frame, for updateQR to
// put in the clipboard.
func (p *chatInfo) copyQR(gtx layout.Context) {
	q := &p.qr
	ops := new(op.Ops)
	sgtx := gtx.Disabled()
	sgtx.Ops = ops
	grad := paint.NewImageOp(q.gradient(q.choice))
	margin := gtx.Dp(qrShotMargin)
	macro := op.Record(ops)
	dims := p.layoutQRCard(sgtx, grad, gtx.Dp(qrCardWidth))
	card := macro.Stop()
	size := dims.Size.Add(image.Pt(2*margin, 2*margin))
	background := clip.Rect{Max: size}.Push(ops)
	fillGradient(ops, grad, size)
	background.Pop()
	at := op.Offset(image.Pt(margin, margin)).Push(ops)
	card.Add(ops)
	at.Pop()

	results := make(chan qrShot, 1)
	q.results = results
	invalidate := p.invalidate
	go func() {
		defer crash.Recover("profile qr code", func(e *crash.Panic) { results <- qrShot{err: e}; invalidate() })
		img, err := renderSnapshot(ops, size)
		var b bytes.Buffer
		if err == nil {
			err = png.Encode(&b, img)
		}
		results <- qrShot{b.Bytes(), err}
		invalidate()
	}()
}

// layoutQR draws the page of the code: the card and the backgrounds,
// which scroll, over the button that copies the card.
func (p *chatInfo) layoutQR(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	q := &p.qr
	sc := scheme(gtx)
	grad := p.renderer.images.Op(q.gradient(q.choice))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return q.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				return p.inColumn(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 16, Right: 16, Top: 8, Bottom: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return p.layoutQRCard(gtx, grad, min(gtx.Constraints.Max.X, gtx.Dp(qrCardWidth)))
								})
							}),
							vspace(20),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return label(gtx, l.T("info.qr_background"), token.TypestyleTitleSmall, sc.Primary.Color, 1)
							}),
							vspace(12),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutQRSwatches(gtx, l) }),
						)
					})
				})
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.inColumn(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 16, Right: 16, Top: 8, Bottom: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return q.copy.Layout(gtx, l.T("info.qr_copy"))
				})
			})
		}),
	)
}

// layoutQRCard draws the card width wide in adaptive Komaru MD3 style:
// concentric QR surface, rounded accent finder patterns, circular dots,
// Komaru SVG center cutout, and username pill.
func (p *chatInfo) layoutQRCard(gtx layout.Context, grad paint.ImageOp, width int) layout.Dimensions {
	q := &p.qr
	if link := p.qrLink(); link != q.link {
		q.link, q.code = link, nil
		if link != "" {
			// Quartile so the center Komaru logo cutout stays reliably scannable.
			q.code, _ = qr.Encode(link, qr.Q)
		}
	}
	sc := scheme(gtx)
	dark := isDark(gtx)
	baseBg := sc.Surface.Color.AsNRGBA()
	if !dark {
		baseBg = color.NRGBA{R: 248, G: 250, B: 252, A: 255}
	}
	accent := token.NewMatColorFromHexRGB(qrGradients[q.choice][2]).AsNRGBA()
	pal := computeKomaruQRPalette(baseBg, accent, dark)

	avatar, ring, pad := gtx.Dp(qrCardAvatar), gtx.Dp(qrAvatarRing), gtx.Dp(qrCardPad)
	// The card starts at the avatar's middle.
	top := ring + avatar/2
	y := top + avatar/2 + gtx.Dp(12)
	// What is on the card is laid out first: its height is the card's.
	macro := op.Record(gtx.Ops)
	if q.code != nil {
		// Whole pixels a module, for a sharp code.
		qrPad := max((width-2*pad)/16, gtx.Dp(10))
		modulePx := max((width-2*pad-2*qrPad)/q.code.Size, 1)
		size := modulePx * q.code.Size
		boxSize := size + 2*qrPad
		boxOrigin := image.Pt((width-boxSize)/2, y)
		innerR := max(gtx.Dp(qrCardRadius)-qrPad*2/3, gtx.Dp(14))
		offset(gtx, boxOrigin, func(gtx layout.Context) layout.Dimensions {
			drawRoundedCard(gtx, image.Pt(boxSize, boxSize), innerR, pal.qrSurface, pal.innerBorder)
			return layout.Dimensions{}
		})
		corner := image.Pt(boxOrigin.X+qrPad, boxOrigin.Y+qrPad)
		logo, gap := qrLogoPx(gtx, size)
		hole := qrHole(q.code.Size, modulePx, logo+2*gap)
		drawKomaruQR(gtx, q.code, corner, modulePx, hole, pal.accent, pal.dotFg)
		holePx := hole.Dx() * modulePx
		logoPx := max(holePx-2*gap, 1)
		holeOrigin := image.Pt(corner.X+hole.Min.X*modulePx, corner.Y+hole.Min.Y*modulePx)
		drawKomaruCenterLogo(gtx, holeOrigin, holePx, logoPx, pal.qrSurface, pal.accent)
		y += boxSize + gtx.Dp(14)
	}
	// Username inside an accent-tinted pill.
	nameText := "@" + strings.ToUpper(p.chatUsername())
	pillPadX, pillPadY := gtx.Dp(14), gtx.Dp(5)
	textMacro := op.Record(gtx.Ops)
	textGtx := gtx
	textGtx.Constraints = layout.Constraints{Max: image.Pt(max(width-2*pad-2*pillPadX, 1), gtx.Constraints.Max.Y)}
	nameDims := label(textGtx, nameText, token.TypestyleTitleMediumEmphasized, token.NewMatColorFromHexRGB(qrGradients[q.choice][2]), 1)
	textCall := textMacro.Stop()
	pillW := min(width-2*pad, nameDims.Size.X+2*pillPadX)
	pillH := nameDims.Size.Y + 2*pillPadY
	pillOrigin := image.Pt((width-pillW)/2, y)
	offset(gtx, pillOrigin, func(gtx layout.Context) layout.Dimensions {
		rr := clip.UniformRRect(image.Rectangle{Max: image.Pt(pillW, pillH)}, pillH/2)
		paint.FillShape(gtx.Ops, pal.pillBg, rr.Op(gtx.Ops))
		offset(gtx, image.Pt((pillW-nameDims.Size.X)/2, pillPadY), func(gtx layout.Context) layout.Dimensions {
			textCall.Add(gtx.Ops)
			return nameDims
		})
		return layout.Dimensions{Size: image.Pt(pillW, pillH)}
	})
	y += pillH + gtx.Dp(20)
	content := macro.Stop()

	offset(gtx, image.Pt(0, top), func(gtx layout.Context) layout.Dimensions {
		drawRoundedCard(gtx, image.Pt(width, y-top), gtx.Dp(qrCardRadius), pal.cardBg, pal.border)
		return layout.Dimensions{}
	})
	content.Add(gtx.Ops)
	around := avatar + 2*ring
	offset(gtx, image.Pt((width-around)/2, 0), func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, pal.cardBg, clip.Ellipse{Max: image.Pt(around, around)}.Op(gtx.Ops))
		return layout.Dimensions{}
	})
	if p.drawAvatar != nil {
		avatarGtx := gtx
		avatarGtx.Constraints = layout.Constraints{Max: image.Pt(avatar, avatar)}
		offset(avatarGtx, image.Pt((width-avatar)/2, ring), func(gtx layout.Context) layout.Dimensions {
			return p.drawAvatar(gtx, p.chat.ID, p.chat.Kind, p.chat.Title, qrCardAvatar)
		})
	}
	return layout.Dimensions{Size: image.Pt(width, y)}
}

// layoutQRSwatches draws the backgrounds to choose from, in rows that
// fill the width.
func (p *chatInfo) layoutQRSwatches(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	q := &p.qr
	if len(q.swatches) != len(qrGradients) {
		q.swatches = make([]surface, len(qrGradients))
	}
	width, gap := gtx.Constraints.Max.X, gtx.Dp(qrSwatchGap)
	cols := max(1, (width+gap)/(gtx.Dp(qrSwatchSize)+gap))
	side := (width - (cols-1)*gap) / cols
	for i := range qrGradients {
		offset(gtx, image.Pt(i%cols*(side+gap), i/cols*(side+gap)), func(gtx layout.Context) layout.Dimensions {
			return p.layoutQRSwatch(gtx, i, side, l.T("info.qr_background")+" "+strconv.Itoa(i+1))
		})
	}
	rows := (len(qrGradients) + cols - 1) / cols
	return layout.Dimensions{Size: image.Pt(width, rows*(side+gap)-gap)}
}

// layoutQRSwatch draws background i as a square of side: its gradient
// under a white tile with the icon of a code, as the card is on it in the
// picture copied; the one chosen is smaller, in a ring.
func (p *chatInfo) layoutQRSwatch(gtx layout.Context, i, side int, title string) layout.Dimensions {
	q := &p.qr
	sc := scheme(gtx)
	size := image.Pt(side, side)
	radius := gtx.Dp(qrSwatchRadius)
	style := surfaceStyle{radius: radius, background: white.SetOpacity(0), content: white, button: title}
	return q.swatches[i].Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		inner := image.Rectangle{Max: size}
		if i == q.choice {
			line := gtx.Dp(2)
			ring := clip.UniformRRect(inner.Inset(line/2), radius-line/2)
			paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Stroke{Path: ring.Path(gtx.Ops), Width: float32(line)}.Op())
			inset := gtx.Dp(5)
			inner, radius = inner.Inset(inset), radius-inset
		}
		func() {
			defer op.Offset(inner.Min).Push(gtx.Ops).Pop()
			defer clip.UniformRRect(image.Rectangle{Max: inner.Size()}, radius).Push(gtx.Ops).Pop()
			fillGradient(gtx.Ops, p.renderer.images.Op(q.gradient(i)), inner.Size())
		}()
		tile, icon := gtx.Dp(32), gtx.Dp(20)
		offset(gtx, image.Pt((side-tile)/2, (side-tile)/2), func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, white, image.Pt(tile, tile), gtx.Dp(8))
			return offset(gtx, image.Pt((tile-icon)/2, (tile-icon)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(icon, icon), func(gtx layout.Context) layout.Dimensions {
					return iconQRCode(gtx, token.NewMatColorFromHexRGB(qrGradients[i][2]))
				})
			})
		})
		return layout.Dimensions{Size: size}
	})
}

// gradientMaterial makes grad, stretched over size from at, what is
// painted next: until the stack it is added in is popped.
func gradientMaterial(ops *op.Ops, grad paint.ImageOp, at f32.Point, size image.Point) {
	src := grad.Size()
	scale := f32.Pt(float32(size.X)/float32(max(1, src.X)), float32(size.Y)/float32(max(1, src.Y)))
	op.Affine(f32.Affine2D{}.Scale(f32.Point{}, scale).Offset(at)).Add(ops)
	grad.Add(ops)
}

// fillGradient fills the clip with grad stretched over size.
func fillGradient(ops *op.Ops, grad paint.ImageOp, size image.Point) {
	defer op.Offset(image.Point{}).Push(ops).Pop()
	gradientMaterial(ops, grad, f32.Point{}, size)
	paint.PaintOp{}.Add(ops)
}

// gradientLabel draws txt in the middle of the width, on two lines at
// most, in the colors of grad from its left to its right: bold whatever
// the theme, whose dark ones lighten their emphasis, the card being white
// in all of them.
func gradientLabel(gtx layout.Context, txt string, style token.Typestyle, grad paint.ImageOp) layout.Dimensions {
	theme := wdk.GetMaterialTheme(gtx)
	info := theme.Typescale[style]
	lbl := widget.Label{Alignment: text.Middle, MaxLines: 2, LineHeight: info.LineHeight, WrapPolicy: text.WrapGraphemes}
	face := font.Font{Typeface: info.Font, Weight: font.Bold}
	// Laid out once for its size, which the gradient is stretched over.
	measure := op.Record(gtx.Ops)
	dims := lbl.Layout(gtx, theme.TextShaper, face, info.Size, txt, op.CallOp{})
	measure.Stop()
	// A line is painted from the start of its baseline: the gradient goes
	// from above the line to under it.
	macro := op.Record(gtx.Ops)
	gradientMaterial(gtx.Ops, grad, f32.Pt(0, -float32(dims.Size.Y)), image.Pt(dims.Size.X, dims.Size.Y*3/2))
	material := macro.Stop()
	return lbl.Layout(gtx, theme.TextShaper, face, info.Size, txt, material)
}
