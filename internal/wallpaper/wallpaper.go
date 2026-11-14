// SPDX-License-Identifier: Unlicense OR MIT

// Package wallpaper tells a window what the desktop shows behind it: the
// wallpaper, blurred, and where the window is over it. A window draws that
// under its translucent surfaces and looks as one the compositor blurs,
// where the compositor cannot, or does it at a cost.
//
// Where the wallpaper is taken from, and how its change is noticed, is the
// system's: the registry and SystemParametersInfo on Windows, the root
// window's pixmap on X11, Tracker's attribute of the desktop's directory on
// Haiku, which Gio's driver reads. Wayland tells a window nothing of its
// place on the screen, so there is nothing to follow there.
package wallpaper

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"komarugram/pkg/resample"
)

// View is a native window: an HWND on Windows, a Window on X11.
type View struct {
	Window uintptr
}

// Screen is a part of the desktop with one picture over all of it.
type Screen struct {
	// Bounds are in the pixels of the desktop.
	Bounds image.Rectangle
	// Picture is the wallpaper as Bounds show it, blurred and several times
	// smaller: it is drawn stretched over them.
	Picture *image.RGBA
}

// Desktop is what is behind the windows. It does not change: a new
// wallpaper is a new Desktop.
type Desktop struct {
	Screens []Screen
}

// Fit is how a picture is placed on a screen.
type Fit int

const (
	// Fill covers the screen, cutting what does not fit.
	Fill Fit = iota
	// Contain shows all of the picture, the color around it.
	Contain
	Stretch
	// Center shows the picture at its size.
	Center
	Tile
	// At shows the picture at its size, its corner at Placement.Offset.
	At
)

// Placement is how a picture is placed on a screen.
type Placement struct {
	Fit Fit
	// Offset is the picture's top left corner on the screen, for At.
	Offset image.Point
}

// pictureSide is the longer side of a screen's picture. The blur leaves no
// detail a larger one would keep.
const pictureSide = 320

// shrink is how many pixels of a screen of the size one of its picture
// stands for, along a side.
func shrink(size image.Point) int {
	return max(1, (max(size.X, size.Y)+pictureSide-1)/pictureSide)
}

// blurRadius is the radius of the blur's box in a picture's pixels, for a
// screen of the size: a blur of about a fortieth of the screen's shorter
// side, 27 pixels on 1920×1080, close to the acrylic of Windows.
func blurRadius(size image.Point, shrink int) int {
	return max(1, (min(size.X, size.Y)/40+shrink/2)/shrink)
}

// Render makes the picture of a screen of the size, in pixels, whose
// wallpaper is src placed so over the color; src may be nil.
func Render(src image.Image, placement Placement, back color.NRGBA, size image.Point) *image.RGBA {
	f := shrink(size)
	w, h := max(1, size.X/f), max(1, size.Y/f)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.NRGBA{R: back.R, G: back.G, B: back.B, A: 0xff}), image.Point{}, draw.Src)
	if src != nil && !src.Bounds().Empty() {
		place(dst, src, placement, f)
	}
	Blur(dst, blurRadius(size, f))
	return dst
}

// place draws src on dst, one pixel of which is shrink of the screen's.
func place(dst *image.RGBA, src image.Image, placement Placement, shrink int) {
	fit := placement.Fit
	b := dst.Bounds()
	iw, ih := float64(src.Bounds().Dx()), float64(src.Bounds().Dy())
	// The picture's size on dst.
	var w, h float64
	switch fit {
	case Stretch:
		w, h = float64(b.Dx()), float64(b.Dy())
	case Center, Tile, At:
		w, h = iw/float64(shrink), ih/float64(shrink)
	default:
		scale := float64(b.Dx()) / iw
		if other := float64(b.Dy()) / ih; (fit == Fill) == (other > scale) {
			scale = other
		}
		w, h = iw*scale, ih*scale
	}
	pw, ph := max(1, int(w+0.5)), max(1, int(h+0.5))
	small := resample.Resize(src, pw, ph)
	sb := small.Bounds()
	if fit == Tile {
		for y := 0; y < b.Dy(); y += ph {
			for x := 0; x < b.Dx(); x += pw {
				draw.Draw(dst, image.Rect(x, y, x+pw, y+ph), small, sb.Min, draw.Over)
			}
		}
		return
	}
	at := image.Pt((b.Dx()-pw)/2, (b.Dy()-ph)/2)
	if fit == At {
		at = placement.Offset.Div(shrink)
	}
	draw.Draw(dst, image.Rectangle{Min: at, Max: at.Add(image.Pt(pw, ph))}, small, sb.Min, draw.Over)
}

// Blur blurs an opaque picture in place: three passes of a box, which
// together are close to a Gaussian. Past its edges the picture goes on as
// its last pixels, so that the edges do not darken.
func Blur(img *image.RGBA, radius int) {
	if radius < 1 {
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	line := make([]uint8, 4*max(w, h))
	for range 3 {
		for y := range h {
			boxLine(img.Pix[y*img.Stride:], 4, w, radius, line)
		}
		for x := range w {
			boxLine(img.Pix[4*x:], img.Stride, h, radius, line)
		}
	}
}

// The look of the compositor's blur on Windows, acrylic, which the window's
// own is made after. Measured on Windows 10 (2026-11-13) with a window of
// surfaces 30% opaque over a wallpaper, drawn both ways at the same places,
// 380 points from black to white:
//
//   - Acrylic is not the blur alone. It keeps the colors about as they are,
//     and draws their brightness toward a dark gray: 19 + 0.70 of the luma,
//     of 255, so that black behind the window is lifted and white dimmed.
//   - The compositor adds what shows through to the window's pixel, which
//     Gio wrote as the surface's color times its opacity, in linear light:
//     the two are added as the bytes they are. A surface over a picture
//     drawn in the window is mixed with it in linear light instead, which
//     comes out darker under a light surface, and lighter under a dark one.
//
// Where the compositor's sum is lighter than the surface over white, the
// window draws white, and is a little darker. Acrylic also lays a fine
// noise over the blur, of about one level of 255, which is not drawn here.
const (
	acrylicLift   = 19.0
	acrylicLuma   = 0.70
	acrylicChroma = 0.95
)

// Acrylic makes of a blurred picture the one to draw under surfaces of the
// color fill that let show of it through, 0 to 1, so that they look as
// they do over the compositor's acrylic.
func Acrylic(src *image.RGBA, fill color.NRGBA, show float64) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	if show <= 0 {
		copy(dst.Pix, src.Pix)
		return dst
	}
	show = min(show, 1)
	var linear [256]float64
	for i := range linear {
		linear[i] = toLinear(float64(i))
	}
	// What the surface adds to the pixel, as the compositor has it, a byte,
	// and as the window mixes it, linear.
	var surface, mixed [3]float64
	for c, v := range [3]uint8{fill.R, fill.G, fill.B} {
		mixed[c] = (1 - show) * linear[v]
		surface[c] = fromLinear(mixed[c])
	}
	for i := 0; i+3 < len(src.Pix); i += 4 {
		p := [3]float64{float64(src.Pix[i]), float64(src.Pix[i+1]), float64(src.Pix[i+2])}
		luma := 0.2126*p[0] + 0.7152*p[1] + 0.0722*p[2]
		for c := range 3 {
			acrylic := acrylicLift + acrylicLuma*luma + acrylicChroma*(p[c]-luma)
			// The pixel the compositor would show, and what the window
			// must draw under the surface for it.
			want := min(max(surface[c]+show*acrylic, 0), 255)
			under := (toLinear(want) - mixed[c]) / show
			dst.Pix[i+c] = uint8(min(max(fromLinear(under), 0), 255) + 0.5)
		}
		dst.Pix[i+3] = 0xff
	}
	return dst
}

// toLinear is the light of an sRGB byte, 0 to 1, and fromLinear the byte
// of a light.
func toLinear(v float64) float64 {
	v /= 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func fromLinear(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v <= 0.0031308 {
		return v * 12.92 * 255
	}
	return (1.055*math.Pow(v, 1/2.4) - 0.055) * 255
}

// boxLine averages n pixels of pix, step bytes apart, over a box of the
// radius, with tmp to keep the line in meanwhile.
func boxLine(pix []uint8, step, n, radius int, tmp []uint8) {
	for i := range n {
		copy(tmp[4*i:4*i+4], pix[i*step:i*step+4])
	}
	at := func(i int) []uint8 {
		i = min(max(i, 0), n-1)
		return tmp[4*i : 4*i+4]
	}
	var sum [4]int
	for i := -radius; i <= radius; i++ {
		p := at(i)
		for c := range 4 {
			sum[c] += int(p[c])
		}
	}
	size := 2*radius + 1
	for i := range n {
		out := pix[i*step : i*step+4]
		for c := range 4 {
			out[c] = uint8((sum[c] + size/2) / size)
		}
		add, drop := at(i+radius+1), at(i-radius)
		for c := range 4 {
			sum[c] += int(add[c]) - int(drop[c])
		}
	}
}
