// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"

	"komarugram/internal/messenger/chattheme"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/resample"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
)

type giftRow struct {
	seen                       uint64
	click                      widget.Clickable
	background                 image.Image
	patternSource, patternTint image.Image
}
type giftDialog struct {
	message model.Message
	list    scroll.List
	close   surface
	retry   surface
	terms   widget.Clickable
	modal   modal
	// told is the failure to load the gift told in the toast last.
	told error
}

// newGiftDialog opens the dialog of the gift of m.
func newGiftDialog(m model.Message) *giftDialog {
	d := &giftDialog{message: m, list: scroll.List{List: layout.List{Axis: layout.Vertical}}}
	d.modal.Open()
	return d
}

func giftGradient(g *model.Gift) image.Image {
	im := image.NewRGBA(image.Rect(0, 0, 128, 128))
	a, b := chattheme.RGB(g.CenterColor), chattheme.RGB(g.EdgeColor)
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			t := min(1, math.Hypot(float64(x)-64, float64(y)-52)/88)
			im.SetRGBA(x, y, color.RGBA{uint8(float64(a.R)*(1-t) + float64(b.R)*t), uint8(float64(a.G)*(1-t) + float64(b.G)*t), uint8(float64(a.B)*(1-t) + float64(b.B)*t), 255})
		}
	}
	return im
}
func tintGiftPattern(im image.Image, rgb uint32) image.Image {
	b := im.Bounds()
	small := resample.Fit(b.Dx(), b.Dy(), image.Pt(64, 64), false)
	if b.Size() != small {
		im = resample.Resize(im, small.X, small.Y)
		b = im.Bounds()
	}
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	col := chattheme.RGB(rgb)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			_, _, _, a := im.At(b.Min.X+x, b.Min.Y+y).RGBA()
			c := col
			c.A = uint8(a>>8) / 3
			out.SetNRGBA(x, y, c)
		}
	}
	return out
}
func (p *chatInfo) giftArt(gtx layout.Context, m model.Message, animate bool) layout.Dimensions {
	size := gtx.Constraints.Max
	if m.Media == nil {
		return layout.Dimensions{Size: size}
	}
	st := p.renderer.media.StatusFit(m, animate, size, false)
	im := st.Frame
	if im == nil {
		im = st.Preview
	}
	if im != nil {
		widget.Image{Src: p.renderer.images.Op(im), Fit: widget.Contain}.Layout(gtx)
	} else if st.Loading {
		diameter := min(gtx.Dp(36), min(size.X, size.Y))
		offset(gtx, image.Pt((size.X-diameter)/2, (size.Y-diameter)/2), func(gtx layout.Context) layout.Dimensions {
			ring(gtx, diameter, 0, animate)
			return layout.Dimensions{}
		})
	}
	return layout.Dimensions{Size: size}
}
func (p *chatInfo) giftCard(gtx layout.Context, m model.Message, l localization.Catalog, animate bool) layout.Dimensions {
	g := m.Gift
	if g == nil {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	r := p.giftRows[m.Key]
	if r == nil {
		r = &giftRow{}
		p.giftRows[m.Key] = r
	}
	r.seen = p.giftGeneration
	if r.click.Clicked(gtx) {
		p.gift = newGiftDialog(m)
	}
	return r.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		size := gtx.Constraints.Max
		bounds := image.Rectangle{Max: size}
		defer clip.UniformRRect(bounds, gtx.Dp(12)).Push(gtx.Ops).Pop()
		fg := scheme(gtx).Surface.OnColor
		if g.HasBackdrop {
			if r.background == nil {
				r.background = giftGradient(g)
			}
			widget.Image{Src: p.renderer.images.Op(r.background), Fit: widget.Fill}.Layout(gtx)
			fg = token.NewMatColorFromHexRGB(g.TextColor)
		} else {
			fillRounded(gtx, scheme(gtx).Surface.Color, size, gtx.Dp(12))
		}
		if g.Pattern != nil && g.HasBackdrop {
			pattern := model.Message{Key: m.Key, Kind: model.MessageSticker, Media: g.Pattern}
			st := p.renderer.media.StatusFit(pattern, false, image.Pt(64, 64), false)
			if st.Frame != nil {
				if r.patternSource != st.Frame {
					r.patternSource = st.Frame
					r.patternTint = tintGiftPattern(st.Frame, g.PatternColor)
				}
				for _, at := range [][2]float32{{.05, .08}, {.62, .02}, {.76, .38}, {.06, .56}, {.60, .74}} {
					box := gtx
					box.Constraints = layout.Exact(image.Pt(gtx.Dp(34), gtx.Dp(34)))
					offset(box, image.Pt(int(at[0]*float32(size.X)), int(at[1]*float32(size.Y))), func(gtx layout.Context) layout.Dimensions {
						return widget.Image{Src: p.renderer.images.Op(r.patternTint), Fit: widget.Contain}.Layout(gtx)
					})
				}
			}
		}

		semantic.LabelOp(g.Title).Add(gtx.Ops)
		art := max(1, min(size.X, size.Y)*65/100)
		box := gtx
		box.Constraints = layout.Exact(image.Pt(art, art))
		offset(box, image.Pt((size.X-art)/2, (size.Y-art)/2+gtx.Dp(4)), func(gtx layout.Context) layout.Dimensions { return p.giftArt(gtx, m, animate) })
		if g.Unique {
			ribbon := image.Pt(gtx.Dp(76), gtx.Dp(18))
			rgtx := gtx
			rgtx.Constraints = layout.Exact(ribbon)
			offset(rgtx, image.Pt(size.X-gtx.Dp(51), -gtx.Dp(8)), func(gtx layout.Context) layout.Dimensions {
				defer op.Affine(f32.Affine2D{}.Rotate(f32.Point{}, math.Pi/4)).Push(gtx.Ops).Pop()
				fillRect(gtx, fg.SetOpacity(.15), ribbon)
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, "#"+strconv.Itoa(g.Number), token.TypestyleLabelSmall, fg, 1)
				})
			})
		}

		// The sender avatar sits above the art and background, inside the top left corner.
		avatarSize := unit.Dp(44)
		av := gtx
		av.Constraints = layout.Exact(image.Pt(gtx.Dp(avatarSize+4), gtx.Dp(avatarSize+4)))
		offset(av, image.Pt(gtx.Dp(4), gtx.Dp(4)), func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, scheme(gtx).Surface.Color, gtx.Constraints.Max, gtx.Dp(avatarSize/2+2))
			return layout.UniformInset(2).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if g.SenderID != 0 && !g.SenderHidden && p.drawAvatar != nil {
					return p.drawAvatar(gtx, g.SenderID, model.KindUser, g.SenderName, avatarSize)
				}
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(32), gtx.Dp(32)))
					return iconPersonal(gtx, scheme(gtx).SurfaceVariant.OnColor)
				})
			})
		})
		return layout.Dimensions{Size: size}
	})
}
func (p *chatInfo) giftsGrid(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	return layout.Inset{Left: 16, Right: 16, Top: 4, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gap := gtx.Dp(12)
		cols := max(1, gtx.Constraints.Max.X/max(1, gtx.Dp(170)))
		rows := (len(p.messages) + cols - 1) / cols
		w := max(1, (gtx.Constraints.Max.X-gap*(cols-1))/cols)
		h := w
		dims := p.list.Layout(gtx, rows+1, func(gtx layout.Context, i int) layout.Dimensions {
			if i == rows {
				return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions { return p.status(gtx, l) })
			}
			for j := 0; j < cols && i*cols+j < len(p.messages); j++ {
				box := gtx
				box.Constraints = layout.Exact(image.Pt(w, h))
				offset(box, image.Pt(j*(w+gap), gap), func(gtx layout.Context) layout.Dimensions { return p.giftCard(gtx, p.messages[i*cols+j], l, animate) })
			}
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h+gap)}
		})
		if p.more && !p.loading && p.problem == nil && p.list.Position.First+p.list.Position.Count >= rows-2 {
			p.load(false)
		}
		return dims
	})
}
func (p *chatInfo) layoutGiftDialog(gtx layout.Context, l localization.Catalog, animate bool) {
	d := p.gift
	if d == nil {
		return
	}
	if d.close.Clicked(gtx) {
		d.modal.Close()
	}
	if d.terms.Clicked(gtx) {
		p.renderer.askLink("https://telegram.org/tos/stars/ua")
	}
	if d.retry.Clicked(gtx) {
		p.renderer.media.Retry(d.message)
	}
	covered := p.renderer.link != ""
	shown := d.modal.Layout(gtx, covered, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(min(gtx.Constraints.Max.X, gtx.Dp(500)), min(gtx.Constraints.Max.Y, gtx.Dp(710)))
		gtx.Constraints = layout.Exact(size)
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(20)).Push(gtx.Ops).Pop()
		fillRect(gtx, scheme(gtx).Surface.Color, size)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 16, Right: 8, Top: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return label(gtx, l.T("gift.title"), token.TypestyleTitleLarge, scheme(gtx).Surface.OnColor, 1)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return navigationButton(gtx, &d.close, iconClear, l.T("viewer.close"))
						}),
					)
				})
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return d.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions { return p.giftDetails(gtx, d, l, animate) })
			}),
		)
	})
	if !shown && p.gift == d {
		p.gift = nil
	}
}

func (p *chatInfo) giftDetails(gtx layout.Context, d *giftDialog, l localization.Catalog, animate bool) layout.Dimensions {
	m := d.message
	g := m.Gift
	if g == nil {
		return layout.Dimensions{}
	}
	return layout.Inset{Left: 24, Right: 24, Bottom: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				n := min(gtx.Constraints.Max.X, gtx.Dp(224))
				gtx.Constraints = layout.Exact(image.Pt(n, n))
				return p.giftArt(gtx, m, animate)
			})
		}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return centeredLabel(gtx, g.Title, token.TypestyleHeadlineSmall, scheme(gtx).Surface.OnColor, 2)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if g.Unique {
					return centeredLabel(gtx, l.Format("gift.number", map[string]string{"index": strconv.Itoa(g.Number)}), token.TypestyleBodyMedium, scheme(gtx).SurfaceVariant.OnColor, 2)
				}
				return layout.Dimensions{}
			}), vspace(16)}
		from := g.SenderName
		if from == "" || g.SenderHidden {
			from = l.T("gift.hidden_sender")
		}
		value := "—"
		if !g.Unique {
			value = l.Count("gift.stars", int(g.Stars), nil)
		}
		fields := [][2]string{{l.T("gift.from"), from}, {l.T("gift.date"), m.Date.Local().Format("02.01.2006 15:04")}, {l.T("gift.value"), value}}
		attribute := func(name string, rarity int) string {
			if rarity > 0 {
				return fmt.Sprintf("%s · %.1f%%", name, float64(rarity)/10)
			}
			return name
		}
		if g.Unique {
			fields = append(fields, [2]string{l.T("gift.model"), attribute(g.Model, g.ModelRarity)}, [2]string{l.T("gift.symbol"), attribute(g.Symbol, g.SymbolRarity)}, [2]string{l.T("gift.backdrop"), attribute(g.Backdrop, g.BackdropRarity)}, [2]string{l.T("gift.quantity"), l.Count("gift.issued", g.Issued, map[string]string{"amount": strconv.Itoa(g.Total)})})
		}
		for _, field := range fields {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 7, Bottom: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Start}.Layout(gtx,
						layout.Flexed(.36, func(gtx layout.Context) layout.Dimensions {
							return label(gtx, field[0], token.TypestyleBodyMedium, scheme(gtx).SurfaceVariant.OnColor, 2)
						}),
						layout.Flexed(.64, func(gtx layout.Context) layout.Dimensions {
							return label(gtx, field[1], token.TypestyleBodyMedium, scheme(gtx).Surface.OnColor, 3)
						}),
					)
				})
			}))
		}
		rows = append(rows, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			text := l.Format("gift.terms", map[string]string{"link": l.T("gift.terms_link")})
			return d.terms.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return centeredLabel(gtx, text, token.TypestyleBodySmall, scheme(gtx).Primary.Color, 0)
			})
		}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if m.Media != nil {
				st := p.renderer.media.StatusFit(m, animate, image.Pt(gtx.Dp(224), gtx.Dp(224)), false)
				if st.Err != nil && st.Err != d.told {
					d.modal.Toast(mediaErrorText(st.Err))
				}
				d.told = st.Err
				if st.Err != nil {
					return textButton(gtx, &d.retry, l.T("history.retry"))
				}
			}
			return layout.Dimensions{}
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

// Drop old presentation textures independently of the media cache. Otherwise
// gift colorization would keep decoder frames alive after that cache evicts them.
func (p *chatInfo) trimGiftRows() {
	for len(p.giftRows) > 96 {
		var key model.MessageKey
		var oldest *giftRow
		for k, r := range p.giftRows {
			if r.seen+1 < p.giftGeneration && (oldest == nil || r.seen < oldest.seen) {
				key, oldest = k, r
			}
		}
		if oldest == nil {
			return
		}
		delete(p.giftRows, key)
	}
}
