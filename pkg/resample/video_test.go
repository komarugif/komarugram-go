// SPDX-License-Identifier: Unlicense OR MIT

package resample

import (
	"image"
	"image/draw"
	"math"
	"strconv"
	"testing"
)

// videoFrame is a 4:2:0 frame with fine detail and a soft-edged disc of
// alpha, as a sticker's.
func videoFrame(w, h int) (*image.YCbCr, *image.YCbCr) {
	im := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	alpha := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	for y := range h {
		for x := range w {
			// Detail finer than a cell, and colours within RGB's gamut.
			im.Y[y*im.YStride+x] = uint8(110 + 60*math.Sin(float64(x)/3)*math.Cos(float64(y)/5) + 20*float64(x)/float64(w))
			dx, dy := float64(x-w/2), float64(y-h/2)
			r := math.Hypot(dx, dy) / float64(min(w, h)/2)
			alpha.Y[y*alpha.YStride+x] = uint8(255 * math.Max(0, math.Min(1, (1-r)*4)))
		}
	}
	for y := range (h + 1) / 2 {
		for x := range (w + 1) / 2 {
			im.Cb[y*im.CStride+x] = uint8(128 + 30*math.Sin(float64(x)/7))
			im.Cr[y*im.CStride+x] = uint8(128 + 30*math.Cos(float64(y)/9))
		}
	}
	return im, alpha
}

// composed is im with its alpha as premultiplied RGBA at its own size.
func composed(im, alpha *image.YCbCr) *image.RGBA {
	out := image.NewRGBA(im.Rect)
	draw.Draw(out, out.Rect, im, image.Point{}, draw.Src)
	if alpha == nil {
		return out
	}
	for y := range out.Rect.Dy() {
		for x := range out.Rect.Dx() {
			a := uint32(alpha.Y[y*alpha.YStride+x])
			p := out.Pix[y*out.Stride+x*4:]
			p[0] = byte(uint32(p[0]) * a / 255)
			p[1] = byte(uint32(p[1]) * a / 255)
			p[2] = byte(uint32(p[2]) * a / 255)
			p[3] = byte(a)
		}
	}
	return out
}

// TestVideoScalerMatchesConvertingFirst checks that scaling the planes
// gives the picture of composing the frame first and scaling that.
func TestVideoScalerMatchesConvertingFirst(t *testing.T) {
	var s VideoScaler
	for _, c := range []struct {
		name         string
		sw, sh, w, h int
		opaque       bool
		// floor is the PSNR to reach.
		floor float64
	}{
		{"cell", 512, 512, 64, 64, false, 40},
		{"larger cell", 512, 512, 128, 128, false, 40},
		{"message", 512, 512, 192, 192, false, 40},
		{"message, scaled", 512, 512, 288, 288, false, 40},
		// Blocks a little larger than the destination's pixels are filtered
		// softer than the frame itself would be, which shows on detail as
		// fine as this, at the edge of what the destination can hold.
		{"odd width", 511, 300, 60, 36, false, 35},
		{"opaque", 256, 256, 40, 40, true, 40},
		{"small step", 200, 200, 150, 150, false, 40},
	} {
		im, alpha := videoFrame(c.sw, c.sh)
		if c.opaque {
			alpha = nil
		}
		want := Resize(composed(im, alpha), c.w, c.h)
		got := s.Resize(im, alpha, c.w, c.h)
		if got.Rect != want.Rect {
			t.Fatalf("%s: size %v, want %v", c.name, got.Rect, want.Rect)
		}
		var se float64
		for i := range want.Pix {
			d := float64(want.Pix[i]) - float64(got.Pix[i])
			se += d * d
		}
		psnr := 10 * math.Log10(255*255/(se/float64(len(want.Pix))+1e-9))
		if psnr < c.floor {
			t.Errorf("%s: PSNR %.1f dB against converting first", c.name, psnr)
		}
		for i := 0; i < len(got.Pix); i += 4 {
			if a := got.Pix[i+3]; got.Pix[i] > a || got.Pix[i+1] > a || got.Pix[i+2] > a {
				t.Fatalf("%s: colour over its alpha at %d", c.name, i/4)
			}
		}
	}
}

// BenchmarkStickerFrame is one frame of a video sticker scaled to the sizes
// a message shows it in, at scales of 1, 1.25, 1.5 and 2, and to cells of a
// panel.
func BenchmarkStickerFrame(b *testing.B) {
	im, alpha := videoFrame(512, 512)
	for _, side := range []int{64, 128, 192, 240, 288, 384} {
		b.Run(strconv.Itoa(side), func(b *testing.B) {
			var s VideoScaler
			dst := image.NewRGBA(image.Rect(0, 0, side, side))
			for b.Loop() {
				s.ResizeInto(dst, im, alpha)
			}
		})
	}
}
