// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// fitMediaSize reserves exactly the same aspect ratio before and after loading.
func fitMediaSize(w, h, maxW, maxH int) image.Point {
	if w <= 0 || h <= 0 {
		w, h = 4, 3
	}
	maxW = max(1, maxW)
	maxH = max(1, maxH)
	width := maxW
	height := max(1, int(int64(width)*int64(h)/int64(w)))
	if height > maxH {
		height = maxH
		width = max(1, int(int64(height)*int64(w)/int64(h)))
	}
	return image.Pt(width, height)
}
func (p *chatPage) mediaLayout(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog, animate bool) layout.Dimensions {
	if m.Kind != model.MessagePhoto && m.Kind != model.MessageVideo && m.Kind != model.MessageGIF && m.Kind != model.MessageSticker {
		return p.fileLayout(gtx, r, m, l)
	}
	maxW, maxH := min(gtx.Constraints.Max.X, gtx.Dp(420)), gtx.Dp(360)
	if m.Kind == model.MessageSticker {
		maxW, maxH = min(maxW, gtx.Dp(stickerMax)), gtx.Dp(stickerMax)
	}
	size := fitMediaSize(m.Media.Width, m.Media.Height, maxW, maxH)
	return p.mediaTile(gtx, r, m, size, false, l, animate)
}
func mediaErrorText(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len([]rune(s)) > 220 {
		s = string([]rune(s)[:220]) + "…"
	}
	return s
}
func (p *chatPage) mediaTile(gtx layout.Context, r *messageRow, m model.Message, size image.Point, crop bool, l localization.Catalog, animate bool) layout.Dimensions {
	end := p.trace.Begin("history.media")
	defer end()
	stickerSet := m.Kind == model.MessageSticker && m.Media.StickerSet != nil
	clicked := r.media.Clicked(gtx)
	if stickerSet {
		clicked = r.sticker.Clicked(gtx)
	}
	if m.Kind == model.MessageSticker {
		animate = animate || r.media.Hovered() || r.sticker.click.Hovered()
	}
	target := m
	video := m.Kind == model.MessageVideo
	if video {
		target.Kind = model.MessagePhoto
		target.Media = m.Media.Thumbnail
	} else if m.Kind == model.MessagePhoto {
		target = r.tileMedia(m, size)
	}
	var frame, preview image.Image
	var err error
	loading, cancelled := false, false
	progress := float32(0)
	if target.Media != nil {
		status := p.media.StatusFit(target, animate, size, crop)
		frame, preview, err = status.Frame, status.Preview, status.Err
		loading, cancelled = status.Loading, status.Cancelled
		if status.Total > 0 {
			progress = min(1, float32(status.Downloaded)/float32(status.Total))
		}
	}
	if err != nil && err != r.mediaTold && !cancelled {
		p.toast.Show(mediaErrorText(err))
	}
	r.mediaTold = err
	if clicked {
		if video {
			p.play(gtx, m, p.reportMedia, l)
		} else if m.Kind == model.MessagePhoto && r.alone && p.openAlone != nil {
			p.openAlone(m)
		} else if m.Kind == model.MessagePhoto && p.openPhoto != nil {
			p.openPhoto(m)
		} else if m.Kind == model.MessageGIF && p.openAlone != nil && err == nil && !cancelled && !loading {
			p.openAlone(m)
		} else if err != nil || cancelled {
			p.media.Retry(target)
		} else if stickerSet {
			p.stickers.open(p, *m.Media.StickerSet)
		} else if loading {
			p.media.Cancel(target)
		}
	}
	draw := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(size)
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Push(gtx.Ops).Pop()
		// A sticker lies on the chat's background once it is drawn; the
		// plate only shows where it is going to be.
		if m.Kind != model.MessageSticker || (frame == nil && preview == nil) {
			fillRounded(gtx, scheme(gtx).SurfaceContainerHigh, size, gtx.Dp(8))
		}
		im := frame
		if im == nil {
			im = preview
		}
		if im != nil {
			fit := widget.Contain
			if crop {
				fit = widget.Cover
			}
			widget.Image{Src: p.images.Op(im), Fit: fit}.Layout(gtx)
		}
		if video || loading || cancelled || err != nil {
			diameter := min(gtx.Dp(48), min(size.X, size.Y))
			origin := image.Pt((size.X-diameter)/2, (size.Y-diameter)/2)
			offset(gtx, origin, func(gtx layout.Context) layout.Dimensions {
				paint.FillShape(gtx.Ops, color.NRGBA{A: 145}, clip.Ellipse{Max: image.Pt(diameter, diameter)}.Op(gtx.Ops))
				// White on the dark circle in either theme.
				white := token.NewMatColorFromHexRGB(0xffffff)
				if video {
					// An icon, not "▶": the text's fonts lack it, and the
					// emoji font drew it as the colored emoji.
					inset := diameter / 6
					offset(gtx, image.Pt(inset, inset), func(gtx layout.Context) layout.Dimensions {
						return exact(gtx, image.Pt(diameter-2*inset, diameter-2*inset), func(gtx layout.Context) layout.Dimensions {
							return iconPlayFile(gtx, white)
						})
					})
					return layout.Dimensions{Size: image.Pt(diameter, diameter)}
				}
				symbol := "×"
				switch {
				case cancelled:
					symbol = "↓"
				case err != nil:
					// A click loads it again; the toast told why it failed.
					symbol = "!"
				}
				box := gtx
				box.Constraints = layout.Exact(image.Pt(diameter, diameter))
				layout.Center.Layout(box, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Point{}
					return label(gtx, symbol, token.TypestyleTitleLarge, white, 1)
				})
				if loading {
					ring(gtx, diameter, progress, animate)
				}
				return layout.Dimensions{Size: image.Pt(diameter, diameter)}
			})
		}
		if video {
			offset(gtx, image.Pt(8, max(0, size.Y-gtx.Dp(25))), func(gtx layout.Context) layout.Dimensions {
				return pill(gtx, fmt.Sprintf("%s · %s", m.Media.Duration.Round(time.Second), l.T("history.video")))
			})
		}
		return layout.Dimensions{Size: size}
	}
	if stickerSet {
		sc := scheme(gtx)
		style := surfaceStyle{radius: gtx.Dp(8), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: l.T("stickers.open")}
		return r.sticker.Layout(gtx, size, style, draw)
	}
	return r.media.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// A press opens or plays it, as a button.
		pointer.CursorPointer.Add(gtx.Ops)
		return draw(gtx)
	})
}

// tileMedia returns the message showing the smallest variant of a photo
// that covers a tile of size pixels. It is kept per row, so drawing a tile
// does not allocate; the blurred preview is the original's.
func (r *messageRow) tileMedia(m model.Message, size image.Point) model.Message {
	if r.tile.Media == nil || r.tileSize != size || r.tile.Key != m.Key {
		variant := *m.Media.Variant(size.X, size.Y)
		if variant.Preview == nil {
			variant.Preview = m.Media.Preview
		}
		r.tile, r.tileSize = m.WithMedia(&variant), size
	}
	return r.tile
}
func ring(gtx layout.Context, diameter int, progress float32, animate bool) {
	start := -math.Pi / 2
	if progress <= 0 {
		progress = .28
		if animate {
			start += float64(gtx.Now.UnixMilli()%1400) * 2 * math.Pi / 1400
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 30)})
		}
	}
	center := float32(diameter) / 2
	radius := center - float32(gtx.Dp(3))
	var path clip.Path
	path.Begin(gtx.Ops)
	steps := max(2, int(progress*64))
	for i := 0; i <= steps; i++ {
		angle := start + float64(progress)*2*math.Pi*float64(i)/float64(steps)
		point := f32.Pt(center+radius*float32(math.Cos(angle)), center+radius*float32(math.Sin(angle)))
		if i == 0 {
			path.MoveTo(point)
		} else {
			path.LineTo(point)
		}
	}
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 240}, clip.Stroke{Path: path.End(), Width: float32(gtx.Dp(2))}.Op())
}
func albumRow(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) *messageRow {
	if r.album == nil {
		r.album = map[model.MessageID]*messageRow{}
	}
	child := r.album[m.Key.MessageID]
	if child == nil || child.revision != m.ContentRevision {
		child = newMessageRow(m, l, gtx.Now)
		r.album[m.Key.MessageID] = child
	}
	child.refreshDates(gtx, m, l)
	return child
}
func (p *chatPage) albumLayout(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog, animate bool) layout.Dimensions {
	width := min(gtx.Constraints.Max.X, gtx.Dp(500))
	gap := gtx.Dp(3)
	y := 0
	for i := 0; i < len(m.Attachments); {
		count := 2
		if len(m.Attachments)%2 == 1 && i == 0 {
			count = 1
		}
		count = min(count, len(m.Attachments)-i)
		aspects := make([]float64, count)
		sum := float64(0)
		for j := 0; j < count; j++ {
			meta := m.Attachments[i+j].Media
			a := 1.
			if meta.Width > 0 && meta.Height > 0 {
				a = float64(meta.Width) / float64(meta.Height)
			}
			a = max(.6, min(a, 1.8))
			aspects[j] = a
			sum += a
		}
		height := min(gtx.Dp(300), max(gtx.Dp(90), int(float64(width-gap*(count-1))/sum)))
		x := 0
		for j := 0; j < count; j++ {
			w := int(float64(width-gap*(count-1)) * aspects[j] / sum)
			if j == count-1 {
				w = width - x
			}
			member := m.Attachments[i+j]
			tileGtx := gtx
			tileGtx.Constraints = layout.Exact(image.Pt(w, height))
			offset(tileGtx, image.Pt(x, y), func(gtx layout.Context) layout.Dimensions {
				return p.mediaTile(gtx, albumRow(gtx, r, member, l), member, image.Pt(w, height), true, l, animate)
			})
			x += w + gap
		}
		y += height + gap
		i += count
	}
	return layout.Dimensions{Size: image.Pt(width, max(0, y-gap))}
}
