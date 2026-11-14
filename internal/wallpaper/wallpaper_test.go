// SPDX-License-Identifier: Unlicense OR MIT

package wallpaper

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// halves is a picture black on the left and white on the right.
func halves(w, h int) *image.RGBA {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(im, im.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	draw.Draw(im, image.Rect(w/2, 0, w, h), image.NewUniform(color.White), image.Point{}, draw.Src)
	return im
}

// TestRenderBlursAndKeepsThePlace checks the picture of a screen: several
// times smaller than it, opaque, the wallpaper's sides where they are on
// the screen, and its edge blurred into a slope.
func TestRenderBlursAndKeepsThePlace(t *testing.T) {
	size := image.Pt(1920, 1080)
	p := Render(halves(3840, 2160), Placement{Fit: Fill}, color.NRGBA{}, size)
	b := p.Bounds()
	if b.Dx() > pictureSide || b.Dx() < pictureSide/2 || b.Dx()*size.Y/size.X != b.Dy() {
		t.Fatalf("the picture is %v for a screen of %v", b.Size(), size)
	}
	y := b.Dy() / 2
	if c := p.RGBAAt(2, y); c.R > 8 || c.A != 0xff {
		t.Errorf("the left is %v, want black", c)
	}
	if c := p.RGBAAt(b.Dx()-3, y); c.R < 247 {
		t.Errorf("the right is %v, want white", c)
	}
	mid := p.RGBAAt(b.Dx()/2, y).R
	if mid < 64 || mid > 192 {
		t.Errorf("the middle is %d, want gray", mid)
	}
	// The blur is a few of the picture's pixels wide.
	if near := p.RGBAAt(b.Dx()/2-2, y).R; near == 0 || near >= mid {
		t.Errorf("two pixels left of the middle is %d, the middle %d: no slope", near, mid)
	}
	for x := 1; x < b.Dx(); x++ {
		if p.RGBAAt(x, y).R < p.RGBAAt(x-1, y).R {
			t.Fatalf("not rising at %d", x)
		}
	}
}

// TestRenderPlaces checks how a picture is placed on a screen it does not
// fit: the color shows around one that is contained or centered, and not
// around one that fills, is stretched or tiled.
func TestRenderPlaces(t *testing.T) {
	size := image.Pt(1600, 800)
	back := color.NRGBA{R: 0xff, A: 0xff}
	src := image.NewRGBA(image.Rect(0, 0, 400, 400))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.NRGBA{B: 0xff, A: 0xff}), image.Point{}, draw.Src)
	for _, tc := range []struct {
		name    string
		fit     Fit
		corner  bool // the color is in the corner
		quarter bool // the color is a quarter of the way along the middle line
	}{
		{"fill", Fill, false, false},
		{"stretch", Stretch, false, false},
		{"tile", Tile, false, false},
		{"contain", Contain, true, false},
		{"center", Center, true, true},
	} {
		p := Render(src, Placement{Fit: tc.fit}, back, size)
		b := p.Bounds()
		red := func(x, y int) bool { c := p.RGBAAt(x, y); return c.R > 200 && c.B < 50 }
		blue := func(x, y int) bool { c := p.RGBAAt(x, y); return c.B > 200 && c.R < 50 }
		if !blue(b.Dx()/2, b.Dy()/2) {
			t.Errorf("%s: the center is %v, want the picture", tc.name, p.RGBAAt(b.Dx()/2, b.Dy()/2))
		}
		if got := red(1, b.Dy()/2); got != tc.corner {
			t.Errorf("%s: the color at the left edge: %t, want %t (%v)", tc.name, got, tc.corner, p.RGBAAt(1, b.Dy()/2))
		}
		if got := red(b.Dx()/2, 1); got != tc.quarter {
			t.Errorf("%s: the color at the top: %t, want %t (%v)", tc.name, got, tc.quarter, p.RGBAAt(b.Dx()/2, 1))
		}
	}
	// A desktop of a color alone.
	p := Render(nil, Placement{Fit: Fill}, back, size)
	if c := p.RGBAAt(3, 3); c != (color.RGBA{R: 0xff, A: 0xff}) {
		t.Errorf("no picture: %v, want the color", c)
	}
}

// TestAcrylicLooksAsOverTheCompositor checks the picture drawn under the
// window's surfaces: a surface mixed with it in linear light, as Gio mixes,
// is the pixel the compositor of Windows shows for the surface over its
// acrylic, the surface's own light as a byte plus the acrylic's share. A
// black wallpaper is lifted, and under a light surface far more than
// under a dark one.
func TestAcrylicLooksAsOverTheCompositor(t *testing.T) {
	light, dark := color.NRGBA{R: 250, G: 246, B: 252, A: 0xff}, color.NRGBA{R: 20, G: 18, B: 24, A: 0xff}
	for _, tc := range []struct {
		fill color.NRGBA
		show float64
		at   color.RGBA
	}{
		{light, 0.7, color.RGBA{A: 0xff}},
		{light, 0.7, color.RGBA{R: 120, G: 100, B: 60, A: 0xff}},
		{light, 0.35, color.RGBA{R: 200, G: 180, B: 100, A: 0xff}},
		{dark, 0.7, color.RGBA{A: 0xff}},
		{dark, 0.35, color.RGBA{R: 200, G: 180, B: 100, A: 0xff}},
		{dark, 0.7, color.RGBA{R: 255, G: 255, B: 255, A: 0xff}},
	} {
		src := image.NewRGBA(image.Rect(0, 0, 1, 1))
		src.SetRGBA(0, 0, tc.at)
		under := Acrylic(src, tc.fill, tc.show).RGBAAt(0, 0)
		in := [3]float64{float64(tc.at.R), float64(tc.at.G), float64(tc.at.B)}
		luma := 0.2126*in[0] + 0.7152*in[1] + 0.0722*in[2]
		for c, fill := range [3]uint8{tc.fill.R, tc.fill.G, tc.fill.B} {
			surface := (1 - tc.show) * toLinear(float64(fill))
			acrylic := acrylicLift + acrylicLuma*luma + acrylicChroma*(in[c]-luma)
			want := min(fromLinear(surface)+tc.show*acrylic, 255)
			drawn := [3]uint8{under.R, under.G, under.B}[c]
			got := fromLinear(surface + tc.show*toLinear(float64(drawn)))
			// The compositor's sum can be lighter than white under the
			// surface is: the window then draws white.
			if d := got - want; d > 1.5 || d < -1.5 && drawn != 255 {
				t.Errorf("fill %v, showing %.2f of %v, channel %d: the window shows %.1f, the compositor %.1f", tc.fill, tc.show, tc.at, c, got, want)
			}
		}
	}
	black := image.NewRGBA(image.Rect(0, 0, 1, 1))
	black.SetRGBA(0, 0, color.RGBA{A: 0xff})
	underLight, underDark := Acrylic(black, light, 0.7).RGBAAt(0, 0), Acrylic(black, dark, 0.7).RGBAAt(0, 0)
	if underLight.R < 60 || underDark.R < 10 || underDark.R > 40 {
		t.Errorf("black is %v under a light surface and %v under a dark one, want about 80 and 20", underLight, underDark)
	}
}

// TestBlurKeepsAFlatPicture checks that the blur does not darken the edges
// or drift: a picture of one color is the same after it.
func TestBlurKeepsAFlatPicture(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 40, 30))
	want := color.RGBA{R: 201, G: 17, B: 99, A: 0xff}
	draw.Draw(im, im.Bounds(), image.NewUniform(want), image.Point{}, draw.Src)
	Blur(im, 5)
	for y := range 30 {
		for x := range 40 {
			if c := im.RGBAAt(x, y); c != want {
				t.Fatalf("at %d,%d: %v, want %v", x, y, c, want)
			}
		}
	}
}
