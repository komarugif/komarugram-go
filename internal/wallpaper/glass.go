// SPDX-License-Identifier: Unlicense OR MIT

package wallpaper

import (
	"image"
	"image/color"
	"math"

	"komarugram/pkg/resample"
)

// Aero's glass, which the DWM of Windows 7 draws behind a window, is not
// acrylic: it is blurred about a tenth as much, tinted with the color of the
// windows the user picked, and has the diagonal streaks of its reflection
// over it. A window that blurs by itself on Windows 7 draws that instead of
// acrylic. The numbers are measured on Windows 7 (docs/UI_COMPONENTS.md,
// "Aero's glass of Windows 7, measured"): every step is in sRGB bytes.

// Glass is the colorization of Aero's glass, as the DWM's key in the
// registry has it.
type Glass struct {
	// Color tints the glass, and Afterglow the light of what is behind it;
	// their alpha does nothing.
	Color, Afterglow color.NRGBA
	// ColorBalance, AfterglowBalance and BlurBalance are how much of each
	// the glass has, 0 to 1.
	ColorBalance, AfterglowBalance, BlurBalance float64
	// Reflection is the intensity of the streaks, 0 to 1.
	Reflection float64
	// Opaque has the glass show nothing of what is behind it, as with the
	// personalization's "Enable transparency" off.
	Opaque bool
}

// SkyGlass is Windows 7's colorization until the user picks another, the
// color Sky at the intensity the panel starts with.
var SkyGlass = Glass{
	Color:            color.NRGBA{R: 0x74, G: 0xb8, B: 0xfc, A: 0xff},
	Afterglow:        color.NRGBA{R: 0x74, G: 0xb8, B: 0xfc, A: 0xff},
	ColorBalance:     0.08,
	AfterglowBalance: 0.43,
	BlurBalance:      0.49,
	Reflection:       0.5,
}

const (
	// glassSigma is the deviation of the glass's blur, in the screen's
	// pixels: a Gaussian reaching about 7 pixels.
	glassSigma = 2.4
	// glassPictureSide is about the longer side of a screen's picture for
	// the glass: its blur keeps detail a picture of acrylic does not.
	glassPictureSide = 1024
	// glassOpaque is what the opaque glass shows in place of the backdrop,
	// and glassOpaqueFrameless the same for a window without the system's
	// frame.
	glassOpaque          = 221
	glassOpaqueFrameless = 223
	// glassParallax: the streaks move with a window by this part of how far
	// its center is from the screen's.
	glassParallax = 8
)

// glassShrink is how many pixels of a screen of the size one of its glass
// picture stands for, along a side.
func glassShrink(size image.Point) int {
	return max(1, (max(size.X, size.Y)+glassPictureSide-1)/glassPictureSide)
}

// RenderGlass makes the picture of a screen of the size for Aero's glass,
// as Render does for acrylic: the wallpaper src placed so over the color,
// blurred as the glass blurs, at about half the screen's size.
func RenderGlass(src image.Image, placement Placement, back color.NRGBA, size image.Point) *image.RGBA {
	f := glassShrink(size)
	w, h := max(1, size.X/f), max(1, size.Y/f)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	fillOpaque(dst, back)
	if src != nil && !src.Bounds().Empty() {
		place(dst, src, placement, f)
	}
	Gaussian(dst, glassSigma/float64(f))
	return dst
}

// StretchReflection makes the streaks of the glass, the image of the
// theme, of the size of a screen's picture: they are stretched over the
// screen. The result is premultiplied.
func StretchReflection(streaks image.Image, size image.Point) *image.RGBA {
	if streaks == nil || streaks.Bounds().Empty() || size.X <= 0 || size.Y <= 0 {
		return nil
	}
	return resample.Resize(streaks, size.X, size.Y)
}

// ReflectionShift is how many pixels of the picture, of the size, of the
// screen at bounds the streaks are moved to the right for a window whose
// center is at centerX on the desktop.
func ReflectionShift(bounds image.Rectangle, picture image.Point, centerX int) int {
	if bounds.Dx() <= 0 {
		return 0
	}
	moved := float64(centerX-(bounds.Min.X+bounds.Dx()/2)) / glassParallax
	return int(math.Round(moved * float64(picture.X) / float64(bounds.Dx())))
}

// GlassState is which glass the DWM draws for a window: the glass of a
// window with the system's frame is another while the window is active,
// and a window without the system's frame has the inactive one's whatever
// it is, with its own streaks.
type GlassState uint8

const (
	// GlassActive is the glass of the active window with the system's
	// frame.
	GlassActive GlassState = iota
	// GlassInactive is the glass of another window with the system's frame.
	GlassInactive
	// GlassFrameless is the glass of a window without the system's frame.
	GlassFrameless
)

// share is how much of the colorization the glass of the state has: an
// active window's all of it, other windows' 0.4, the rest the backdrop.
func (s GlassState) share() float64 {
	if s == GlassActive {
		return 1
	}
	return 0.4
}

// reflection is how strong the streaks of the state are at the intensity:
// at most their own alpha without the system's frame, and 0.745 of it with.
func (s GlassState) reflection(intensity float64) float64 {
	switch s {
	case GlassActive:
		return 0.745 * min(1.79*intensity, 1)
	case GlassInactive:
		return 0.745 * min(1.49*intensity, 1)
	}
	return min(1.49*intensity, 1)
}

// over is the glass of the state over a pixel of the blurred backdrop, in
// bytes.
func (g Glass) over(state GlassState, p [3]float64) [3]float64 {
	return g.mix(state).over(p)
}

// glassMix is the glass as factors: a pixel of it is backdrop times the
// pixel, plus tint, plus afterglow times the pixel's luma.
type glassMix struct {
	backdrop        float64
	tint, afterglow [3]float64
}

// mix is the glass of the state: the colorization's share of it is
// share·(BlurBalance·backdrop + ColorBalance·Color), the backdrop is the
// rest, and the afterglow is added to all of it.
func (g Glass) mix(state GlassState) glassMix {
	var m glassMix
	share := state.share()
	c := [3]float64{float64(g.Color.R), float64(g.Color.G), float64(g.Color.B)}
	a := [3]float64{float64(g.Afterglow.R), float64(g.Afterglow.G), float64(g.Afterglow.B)}
	if g.Opaque {
		// What is behind the glass is not seen.
		white := float64(glassOpaque)
		if state == GlassFrameless {
			white = glassOpaqueFrameless
		}
		for i := range 3 {
			m.tint[i] = white*(1-share*g.ColorBalance) + share*g.ColorBalance*c[i]
		}
		return m
	}
	m.backdrop = 1 - share + share*g.BlurBalance
	for i := range 3 {
		m.tint[i] = share * g.ColorBalance * c[i]
		m.afterglow[i] = g.AfterglowBalance * a[i] / 255
	}
	return m
}

func (m glassMix) over(p [3]float64) [3]float64 {
	luma := 0.2126*p[0] + 0.7152*p[1] + 0.0722*p[2]
	var out [3]float64
	for i := range out {
		out[i] = min(max(m.backdrop*p[i]+m.tint[i]+m.afterglow[i]*luma, 0), 255)
	}
	return out
}

// GlassUnder makes, into dst when it is of the size, the picture to draw
// under surfaces of the color fill that let show of it through, 0 to 1,
// so that they look as over the glass of Windows 7, of the state, the
// screen's picture is behind: its streaks, when it has them, moved shift
// pixels to the right. The picture of the screen is one RenderGlass made.
func GlassUnder(dst *image.RGBA, s Screen, g Glass, state GlassState, shift int, fill color.NRGBA, show float64) *image.RGBA {
	src := s.Picture
	b := src.Bounds()
	if dst == nil || dst.Bounds() != b {
		dst = image.NewRGBA(b)
	}
	show = min(max(show, 0), 1)
	under := underTables(fill, show)
	k := state.reflection(g.Reflection)
	r := s.Reflection
	if r != nil && (r.Bounds().Size() != b.Size() || k <= 0) {
		r = nil
	}
	m := g.mix(state)
	w, h := b.Dx(), b.Dy()
	for y := range h {
		row := src.Pix[y*src.Stride : y*src.Stride+4*w]
		out := dst.Pix[y*dst.Stride : y*dst.Stride+4*w]
		var streaks []uint8
		if r != nil {
			streaks = r.Pix[y*r.Stride : y*r.Stride+4*w]
		}
		for x := range w {
			i := 4 * x
			v := m.over([3]float64{float64(row[i]), float64(row[i+1]), float64(row[i+2])})
			if streaks != nil {
				j := 4 * min(max(x-shift, 0), w-1)
				if a := k * float64(streaks[j+3]) / 255; a > 0 {
					for c := range 3 {
						v[c] = min(v[c]*(1-a)+k*float64(streaks[j+c]), 255)
					}
				}
			}
			for c := range 3 {
				out[i+c] = under[c][int(v[c]*underSteps+0.5)]
			}
			out[i+3] = 0xff
		}
	}
	return dst
}

// underSteps is how finely a level of 255 is told apart in underTables.
const underSteps = 4

// underTables are, for each channel, the byte to draw under a surface of
// the color fill that lets show of it through, for the compositor to show
// the surface over a backdrop of a level, in steps of 1/underSteps: the
// compositor adds the backdrop's share to the surface's light as bytes,
// where Gio mixes the surface with what is drawn under it in linear light.
// Where the compositor's sum is lighter than the surface over white, the
// window draws white.
func underTables(fill color.NRGBA, show float64) [3][]uint8 {
	var t [3][]uint8
	for c, v := range [3]uint8{fill.R, fill.G, fill.B} {
		t[c] = make([]uint8, 255*underSteps+1)
		if show <= 0 {
			for i := range t[c] {
				t[c][i] = uint8(i / underSteps)
			}
			continue
		}
		mixed := (1 - show) * toLinear(float64(v))
		surface := fromLinear(mixed)
		for i := range t[c] {
			want := min(surface+show*float64(i)/underSteps, 255)
			under := (toLinear(want) - mixed) / show
			t[c][i] = uint8(min(max(fromLinear(under), 0), 255) + 0.5)
		}
	}
	return t
}

// Gaussian blurs an opaque picture in place by a Gaussian of the deviation
// sigma, in its pixels. Past its edges the picture goes on as its last
// pixels.
func Gaussian(img *image.RGBA, sigma float64) {
	if sigma < 0.2 {
		return
	}
	radius := int(math.Ceil(3 * sigma))
	weights := make([]float64, 2*radius+1)
	sum := 0.0
	for i := range weights {
		d := float64(i - radius)
		weights[i] = math.Exp(-d * d / (2 * sigma * sigma))
		sum += weights[i]
	}
	for i := range weights {
		weights[i] /= sum
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	line := make([]float64, 4*max(w, h))
	pass := func(pix []uint8, step, n int) {
		for i := range n {
			for c := range 4 {
				line[4*i+c] = float64(pix[i*step+c])
			}
		}
		for i := range n {
			var acc [4]float64
			for k, wt := range weights {
				j := min(max(i+k-radius, 0), n-1)
				for c := range 4 {
					acc[c] += wt * line[4*j+c]
				}
			}
			for c := range 4 {
				pix[i*step+c] = uint8(acc[c] + 0.5)
			}
		}
	}
	for y := range h {
		pass(img.Pix[y*img.Stride:], 4, w)
	}
	for x := range w {
		pass(img.Pix[4*x:], img.Stride, h)
	}
}

// fillOpaque fills dst with the color, opaque.
func fillOpaque(dst *image.RGBA, back color.NRGBA) {
	for i := 0; i+3 < len(dst.Pix); i += 4 {
		dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = back.R, back.G, back.B, 0xff
	}
}

// Without Aero, under the Basic and Classic themes, Windows 7 offers no
// color for the windows (its Window Color is the classic dialog there), and
// the glass keeps the one the theme wrote, Sky: the client offers the
// personalization's colors and its intensity in its settings then.

// WindowColors are the colors the personalization of Windows 7 offers for
// the windows, in its order (Sky, Twilight, Sea, Leaf, Lime, Sun, Pumpkin,
// Ruby, Fuchsia, Blush, Violet, Lavender, Taupe, Chocolate, Slate, Frost),
// as the DWM keeps them, 0xAARRGGBB, each at the intensity it starts with:
// its alpha (measured 2026-11-15, by the DWM's colorization once each is
// clicked).
var WindowColors = [16]uint32{
	0x6b74b8fc, 0xa80046ad, 0x8032cdcd, 0x6614a600,
	0x6697d937, 0x54fadc0e, 0x80ff9c00, 0xa8ce0f0f,
	0x66ff0099, 0x70fcc7f8, 0x856e3ba1, 0x528d5a94,
	0x6698844c, 0xa84f1b1b, 0x80555555, 0x54fcfcfc,
}

// IntensityMin and IntensityMax are the ends of the personalization's
// intensity slider, the alpha of the color.
const (
	IntensityMin = 0x1a
	IntensityMax = 0xd9
)

// GlassOfColor is the glass of a color as the personalization of Windows 7
// sets it: the color and the afterglow alike, the balances made of the
// intensity, its alpha, the reflection at half. The balances are as the
// personalization writes them for every one of its colors and both ends
// of its slider (5, 5 and 90 at 0x1a; 85, 0 and 15 at 0xd9).
func GlassOfColor(argb uint32) Glass {
	c := color.NRGBA{R: uint8(argb >> 16), G: uint8(argb >> 8), B: uint8(argb), A: 0xff}
	balance := int((float64(argb>>24)/255-0.1)/0.75*100 + 10)
	var colorBalance, afterglow, blur int
	switch {
	case balance < 50:
		colorBalance, blur = 5, 100-balance
		afterglow = 100 - colorBalance - blur
	case balance >= 95:
		afterglow, colorBalance = 0, balance-25
		blur = 100 - colorBalance
	default:
		afterglow, blur = 95-balance, 50-(balance-50)/2
		colorBalance = 100 - afterglow - blur
	}
	return Glass{
		Color:            c,
		Afterglow:        c,
		ColorBalance:     float64(colorBalance) / 100,
		AfterglowBalance: float64(afterglow) / 100,
		BlurBalance:      float64(blur) / 100,
		Reflection:       0.5,
	}
}
