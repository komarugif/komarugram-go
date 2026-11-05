// SPDX-License-Identifier: Unlicense OR MIT

package resample

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// planes is a frame of one color, its levels as a video's encoder made them, from the
// matrix and the range of colors.
func planes(w, h int, r, g, b uint8, colors Colors) *image.YCbCr {
	kr, kb := float64(0.299), float64(0.114)
	switch colors.Matrix {
	case BT709:
		kr, kb = 0.2126, 0.0722
	case BT2020:
		kr, kb = 0.2627, 0.0593
	}
	kg := 1 - kr - kb
	fr, fg, fb := float64(r)/255, float64(g)/255, float64(b)/255
	y := kr*fr + kg*fg + kb*fb
	u := (fb - y) / (2 * (1 - kb))
	v := (fr - y) / (2 * (1 - kr))
	ly, lc, black := 255.0, 255.0, 0.0
	if colors.Limited {
		ly, lc, black = 219, 224, 16
	}
	im := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	for i := range im.Y {
		im.Y[i] = uint8(black + y*ly + .5)
	}
	for i := range im.Cb {
		im.Cb[i], im.Cr[i] = uint8(128+u*lc+.5), uint8(128+v*lc+.5)
	}
	return im
}

var testColors = []color.RGBA{
	{192, 32, 32, 255}, {32, 192, 32, 255}, {32, 32, 192, 255}, {208, 160, 128, 255}, {0, 0, 0, 255}, {255, 255, 255, 255},
}

func near(t *testing.T, name string, got, want color.RGBA) {
	t.Helper()
	diff := func(a, b uint8) int {
		d := int(a) - int(b)
		return max(d, -d)
	}
	if diff(got.R, want.R) > 3 || diff(got.G, want.G) > 3 || diff(got.B, want.B) > 3 || got.A != want.A {
		t.Errorf("%s: %v, not %v", name, got, want)
	}
}

// Each way a video's levels are made comes back as the color it was: at its own size,
// scaled, and scaled with alpha, which goes by another path.
func TestVideoColors(t *testing.T) {
	for _, colors := range []Colors{
		{}, {Matrix: BT709}, {Limited: true}, {Matrix: BT709, Limited: true}, {Matrix: BT2020, Limited: true},
	} {
		for _, c := range testColors {
			im := planes(64, 64, c.R, c.G, c.B, colors)
			s := VideoScaler{Colors: colors}
			own := s.Resize(im, nil, 64, 64)
			near(t, "its own size", own.RGBAAt(32, 32), c)
			small := s.Resize(im, nil, 20, 20)
			near(t, "scaled", small.RGBAAt(10, 10), c)
			mask := image.NewYCbCr(im.Rect, image.YCbCrSubsampleRatio420)
			for i := range mask.Y {
				mask.Y[i] = 255
			}
			near(t, "with alpha", s.Resize(im, mask, 20, 20).RGBAAt(10, 10), c)
			near(t, "with alpha, its own size", s.Resize(im, mask, 64, 64).RGBAAt(32, 32), c)
		}
	}
}

// With half alpha the color is premultiplied, black being black in every range.
func TestVideoColorsPremultiplied(t *testing.T) {
	colors := Colors{Matrix: BT709, Limited: true}
	im := planes(64, 64, 0, 0, 0, colors)
	mask := image.NewYCbCr(im.Rect, image.YCbCrSubsampleRatio420)
	for i := range mask.Y {
		mask.Y[i] = 128
	}
	s := VideoScaler{Colors: colors}
	got := s.Resize(im, mask, 20, 20).RGBAAt(10, 10)
	near(t, "half alpha", got, color.RGBA{0, 0, 0, 128})
	// And white, half of it: what black's level is taken from counts by its alpha.
	got = s.Resize(planes(64, 64, 255, 255, 255, colors), mask, 20, 20).RGBAAt(10, 10)
	near(t, "half alpha, white", got, color.RGBA{128, 128, 128, 128})
}

// JPEG's, the zero value, stays as image/draw has it: photos do not change.
func TestConvertZeroIsJPEG(t *testing.T) {
	im := planes(16, 16, 200, 80, 40, Colors{})
	want := image.NewRGBA(im.Rect)
	draw.Draw(want, want.Rect, im, image.Point{}, draw.Src)
	got := image.NewRGBA(im.Rect)
	Convert(got, im, Colors{})
	for i := range got.Pix {
		d := int(got.Pix[i]) - int(want.Pix[i])
		if d > 1 || d < -1 {
			t.Fatalf("byte %d: %d, not %d", i, got.Pix[i], want.Pix[i])
		}
	}
}

func BenchmarkConvertHD(b *testing.B) {
	im, _ := videoFrame(1280, 720)
	dst := image.NewRGBA(im.Rect)
	colors := Colors{Matrix: BT709, Limited: true}
	for b.Loop() {
		Convert(dst, im, colors)
	}
}

func BenchmarkDrawHD(b *testing.B) {
	im, _ := videoFrame(1280, 720)
	dst := image.NewRGBA(im.Rect)
	for b.Loop() {
		draw.Draw(dst, dst.Rect, im, image.Point{}, draw.Src)
	}
}
