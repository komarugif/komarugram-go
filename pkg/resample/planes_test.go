// SPDX-License-Identifier: Unlicense OR MIT

package resample

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// TestPlanesMatchResize checks that a frame scaled into planes and
// converted is the picture the scaler makes at once.
func TestPlanesMatchResize(t *testing.T) {
	for _, c := range []struct {
		name         string
		sw, sh, w, h int
		opaque       bool
	}{
		// The sizes a loop is kept in planes at: small ones fit as RGBA,
		// and chroma at half of a small size is coarse for this detail.
		{"message", 512, 512, 192, 192, false},
		{"message, scaled", 512, 512, 288, 288, false},
		{"odd sizes", 511, 300, 241, 141, false},
		{"its own size", 64, 64, 64, 64, false},
		{"opaque", 256, 256, 160, 160, true},
	} {
		for _, colors := range []Colors{{}, {Matrix: BT709, Limited: true}} {
			im, alpha := videoFrame(c.sw, c.sh)
			if c.opaque {
				alpha = nil
			}
			s := VideoScaler{Colors: colors}
			// Through the path of frames with alpha, as the planes go.
			mask := alpha
			if mask == nil {
				mask = image.NewYCbCr(im.Rect, image.YCbCrSubsampleRatio420)
				for i := range mask.Y {
					mask.Y[i] = 255
				}
			}
			want := s.Resize(im, mask, c.w, c.h)
			planes := s.ResizePlanes(im, alpha, c.w, c.h)
			if planes.Rect != want.Rect || planes.SubsampleRatio != image.YCbCrSubsampleRatio420 {
				t.Fatalf("%s: planes %v %v", c.name, planes.Rect, planes.SubsampleRatio)
			}
			got := image.NewRGBA(want.Rect)
			ConvertAlpha(got, planes, colors)
			var se float64
			for i := range want.Pix {
				d := float64(want.Pix[i]) - float64(got.Pix[i])
				se += d * d
			}
			if psnr := 10 * math.Log10(255*255/(se/float64(len(want.Pix))+1e-9)); psnr < 40 {
				t.Errorf("%s, %+v: PSNR %.1f dB against the picture scaled at once", c.name, colors, psnr)
			}
			for i := 0; i < len(got.Pix); i += 4 {
				if a := got.Pix[i+3]; got.Pix[i] > a || got.Pix[i+1] > a || got.Pix[i+2] > a {
					t.Fatalf("%s: colour over its alpha at %d", c.name, i/4)
				}
				if got.Pix[i+3] != want.Pix[i+3] {
					t.Fatalf("%s: alpha %d at %d, want %d", c.name, got.Pix[i+3], i/4, want.Pix[i+3])
				}
			}
		}
	}
}

// TestPlanesColors checks the colours of planes, whole and at half alpha,
// in the ranges and matrices of video.
func TestPlanesColors(t *testing.T) {
	for _, colors := range []Colors{{}, {Matrix: BT709}, {Limited: true}, {Matrix: BT709, Limited: true}} {
		for _, c := range testColors {
			im := planes(64, 64, c.R, c.G, c.B, colors)
			mask := image.NewYCbCr(im.Rect, image.YCbCrSubsampleRatio420)
			for _, a := range []uint8{255, 128} {
				for i := range mask.Y {
					mask.Y[i] = a
				}
				s := VideoScaler{Colors: colors}
				got := image.NewRGBA(image.Rect(0, 0, 20, 20))
				ConvertAlpha(got, s.ResizePlanes(im, mask, 20, 20), colors)
				half := func(v uint8) uint8 { return uint8((int(v)*int(a) + 127) / 255) }
				near(t, "planes", got.RGBAAt(10, 10), color.RGBA{half(c.R), half(c.G), half(c.B), a})
			}
		}
	}
}

// BenchmarkConvertAlphaSticker is a picture of a loop kept in planes made
// for a frame, at a message's size.
func BenchmarkConvertAlphaSticker(b *testing.B) {
	im, alpha := videoFrame(512, 512)
	var s VideoScaler
	planes := s.ResizePlanes(im, alpha, 192, 192)
	dst := image.NewRGBA(planes.Rect)
	for b.Loop() {
		ConvertAlpha(dst, planes, Colors{})
	}
}
