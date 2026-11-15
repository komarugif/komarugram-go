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

// TestGlassIsWhatTheDWMDrew checks the glass of Windows 7 against what its
// DWM drew over a backdrop of one color, at several settings of the
// colorization (docs/UI_COMPONENTS.md, "Aero's glass of Windows 7,
// measured"): in a window with the system's frame, active and not, and in
// one without it; each balance changed alone, another color, and the
// opaque glass.
func TestGlassIsWhatTheDWMDrew(t *testing.T) {
	orange := color.NRGBA{R: 0xff, G: 0x9c, A: 0xff}
	base := Glass{Color: orange, Afterglow: orange, ColorBalance: 0.24, AfterglowBalance: 0.32, BlurBalance: 0.44, Reflection: 0.5}
	with := func(change func(g *Glass)) Glass {
		g := base
		change(&g)
		return g
	}
	noBlur := with(func(g *Glass) { g.BlurBalance = 0 })
	allColor := with(func(g *Glass) { g.ColorBalance = 1 })
	afterglow := with(func(g *Glass) { g.AfterglowBalance = 0.64 })
	opaque := with(func(g *Glass) { g.Opaque, g.ColorBalance = true, 0.6 })
	blue := with(func(g *Glass) {
		g.Color, g.Afterglow = color.NRGBA{R: 0x40, G: 0x80, B: 0xff, A: 0xff}, color.NRGBA{G: 0xff, B: 0x40, A: 0xff}
	})
	gray, red, bluish := [3]float64{120, 120, 120}, [3]float64{255, 0, 0}, [3]float64{0, 0, 255}
	for _, tc := range []struct {
		name  string
		glass Glass
		state GlassState
		under [3]float64
		drew  [3]float64
	}{
		{"active", base, GlassActive, gray, [3]float64{150.4, 112.8, 52.1}},
		{"active", base, GlassActive, red, [3]float64{189.3, 47.0, 0}},
		{"active", base, GlassActive, bluish, [3]float64{65.9, 40.2, 110.8}},
		{"active, no blur balance", noBlur, GlassActive, gray, [3]float64{98.7, 60.1, 0}},
		{"active, twice the afterglow", afterglow, GlassActive, gray, [3]float64{189.3, 135.5, 52.1}},
		{"active, opaque", opaque, GlassActive, gray, [3]float64{239.8, 181.2, 88.0}},
		{"inactive", base, GlassInactive, gray, [3]float64{155.7, 131.6, 93.2}},
		{"inactive", base, GlassInactive, red, [3]float64{240.1, 25.4, 0}},
		{"inactive, all the color", allColor, GlassInactive, gray, [3]float64{232.4, 177.6, 93.2}},
		{"inactive, all the color", allColor, GlassInactive, bluish, [3]float64{107.1, 65.1, 197.6}},
		{"inactive, opaque", with(func(g *Glass) { g.Opaque = true }), GlassInactive, gray, [3]float64{224.3, 215.4, 200.5}},
		{"frameless", base, GlassFrameless, gray, [3]float64{157.0, 132.7, 94.1}},
		{"frameless, no blur balance", noBlur, GlassFrameless, red, [3]float64{195.9, 26.0, 0}},
		{"frameless, another color", blue, GlassFrameless, gray, [3]float64{100.0, 144.8, 128.7}},
		{"frameless, opaque", opaque, GlassFrameless, bluish, [3]float64{230.9, 207.0, 168.9}},
	} {
		got := tc.glass.over(tc.state, tc.under)
		for c := range 3 {
			// The formula's round numbers are within two levels of what
			// was measured at each setting alone.
			if d := got[c] - tc.drew[c]; d > 2.5 || d < -2.5 {
				t.Errorf("%s, over %v: %.1f, the DWM drew %.1f", tc.name, tc.under, got, tc.drew)
				break
			}
		}
	}
}

// TestGlassUnderLooksAsOverTheDWM checks the picture drawn under the
// window's surfaces on Windows 7: a surface mixed with it in linear light,
// as Gio mixes, is the pixel the DWM shows for the surface over its glass,
// streaks and all, the surface's light as a byte plus the glass's share.
func TestGlassUnderLooksAsOverTheDWM(t *testing.T) {
	g := SkyGlass
	picture := image.NewRGBA(image.Rect(0, 0, 2, 1))
	picture.SetRGBA(0, 0, color.RGBA{R: 30, G: 90, B: 160, A: 0xff})
	picture.SetRGBA(1, 0, color.RGBA{R: 230, G: 200, B: 120, A: 0xff})
	// A streak of white at half its alpha over the second pixel.
	streaks := image.NewRGBA(picture.Bounds())
	streaks.SetRGBA(1, 0, color.RGBA{R: 128, G: 128, B: 128, A: 128})
	s := Screen{Picture: picture, Reflection: streaks}
	for _, fill := range []color.NRGBA{{R: 250, G: 246, B: 252, A: 0xff}, {R: 20, G: 18, B: 24, A: 0xff}} {
		for _, show := range []float64{0.35, 0.7} {
			under := GlassUnder(nil, s, g, GlassFrameless, 0, fill, show)
			for x := range 2 {
				p := picture.RGBAAt(x, 0)
				glass := g.over(GlassFrameless, [3]float64{float64(p.R), float64(p.G), float64(p.B)})
				if x == 1 {
					k := GlassFrameless.reflection(g.Reflection)
					for c := range 3 {
						glass[c] = glass[c]*(1-k*128/255) + k*128
					}
				}
				drawn := under.RGBAAt(x, 0)
				for c, f := range [3]uint8{fill.R, fill.G, fill.B} {
					surface := (1 - show) * toLinear(float64(f))
					want := min(fromLinear(surface)+show*glass[c], 255)
					v := [3]uint8{drawn.R, drawn.G, drawn.B}[c]
					got := fromLinear(surface + show*toLinear(float64(v)))
					if d := got - want; d > 1.5 || d < -1.5 && v != 255 {
						t.Errorf("fill %v, showing %.2f, pixel %d, channel %d: the window shows %.1f, the DWM %.1f", fill, show, x, c, got, want)
					}
				}
			}
		}
	}
}

// TestReflectionMovesAnEighthWithTheWindow checks where the streaks are:
// stretched over the screen for a window at its center, and moved with a
// window by an eighth of how far it went.
func TestReflectionMovesAnEighthWithTheWindow(t *testing.T) {
	screen := image.Rect(1920, 0, 3840, 1080)
	picture := image.Pt(960, 540)
	if got := ReflectionShift(screen, picture, 2880); got != 0 {
		t.Errorf("a window at the screen's center moves the streaks by %d", got)
	}
	// 160 pixels of the screen are 80 of the picture; an eighth is 10.
	if got := ReflectionShift(screen, picture, 2880+160); got != 10 {
		t.Errorf("a window moved right by 160 moves the streaks by %d, want 10", got)
	}
	if got := ReflectionShift(screen, picture, 2880-160); got != -10 {
		t.Errorf("a window moved left by 160 moves the streaks by %d, want -10", got)
	}
}

// TestGlassOfColorIsWhatWindowsSets checks the glass of a color of the
// personalization of Windows 7: the balances it wrote for its colors, each
// at its own intensity, and at both ends of its slider (measured
// 2026-11-15).
func TestGlassOfColorIsWhatWindowsSets(t *testing.T) {
	for _, tc := range []struct {
		argb                   uint32
		color, afterglow, blur int
	}{
		{0x6b74b8fc, 8, 43, 49},  // Sky
		{0xa80046ad, 56, 11, 33}, // Twilight
		{0x8032cdcd, 24, 32, 44}, // Sea
		{0x6614a600, 5, 45, 50},  // Leaf
		{0x54fadc0e, 5, 35, 60},  // Sun
		{0x70fcc7f8, 12, 40, 48}, // Blush
		{0x856e3ba1, 29, 29, 42}, // Violet
		{0x528d5a94, 5, 34, 61},  // Lavender
		{0x1a75b8fa, 5, 5, 90},   // the slider at its least
		{0xd975b8fa, 85, 0, 15},  // and at its most
	} {
		g := GlassOfColor(tc.argb)
		got := [3]int{int(g.ColorBalance*100 + 0.5), int(g.AfterglowBalance*100 + 0.5), int(g.BlurBalance*100 + 0.5)}
		if want := [3]int{tc.color, tc.afterglow, tc.blur}; got != want {
			t.Errorf("%08x: balances %v, Windows sets %v", tc.argb, got, want)
		}
		if c := g.Color; c != g.Afterglow || c.R != uint8(tc.argb>>16) || c.G != uint8(tc.argb>>8) || c.B != uint8(tc.argb) {
			t.Errorf("%08x: color %v, afterglow %v", tc.argb, c, g.Afterglow)
		}
	}
}
