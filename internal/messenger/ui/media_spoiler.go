// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/draw"

	"gio-mw/token"

	"gioui.org/layout"
)

var spoilerDim = token.NewMatColorFromHexRGBA(0x00000040)

// paintMediaSpoiler draws the same darkening and particles over the
// blurred attachment in the history and in the send-files preview.
func paintMediaSpoiler(gtx layout.Context, size image.Point, index int) {
	fillRect(gtx, spoilerDim, size)
	paintMediaDust(gtx, size, index)
}

// blurredPicture is src made small and blurred, which drawn at the size of
// src shows its colors and nothing of what is in it, as Telegram Desktop
// blurs media under a spoiler.
func blurredPicture(im image.Image) *image.RGBA {
	const side = 24
	r := im.Bounds()
	w, h := r.Dx(), r.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	src, ok := im.(*image.RGBA)
	if !ok {
		src = image.NewRGBA(r)
		draw.Draw(src, r, im, r.Min, draw.Src)
	}
	dw, dh := max(1, w*side/max(w, h)), max(1, h*side/max(w, h))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	// Each pixel is the mean of the pixels of src it covers.
	for y := range dh {
		y0, y1 := y*h/dh, max(y*h/dh+1, (y+1)*h/dh)
		for x := range dw {
			x0, x1 := x*w/dw, max(x*w/dw+1, (x+1)*w/dw)
			var sum [4]int
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[src.PixOffset(r.Min.X+x0, r.Min.Y+sy):]
				for sx := range x1 - x0 {
					for c := range 4 {
						sum[c] += int(row[sx*4+c])
					}
				}
			}
			n := (y1 - y0) * (x1 - x0)
			for c := range 4 {
				dst.Pix[y*dst.Stride+x*4+c] = uint8(sum[c] / n)
			}
		}
	}
	// Then twice through a box of three each way, for the steps between
	// its pixels not to show once drawn large.
	tmp := image.NewRGBA(dst.Rect)
	for range 2 {
		for _, pass := range [2]image.Point{{1, 0}, {0, 1}} {
			for y := range dh {
				for x := range dw {
					for c := range 4 {
						sum := 0
						for k := -1; k <= 1; k++ {
							sx, sy := min(max(x+k*pass.X, 0), dw-1), min(max(y+k*pass.Y, 0), dh-1)
							sum += int(dst.Pix[sy*dst.Stride+sx*4+c])
						}
						tmp.Pix[y*tmp.Stride+x*4+c] = uint8((sum + 1) / 3)
					}
				}
			}
			dst, tmp = tmp, dst
		}
	}
	return dst
}
