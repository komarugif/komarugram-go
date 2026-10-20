// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"math"
	"sync"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// shadowLayers is how many layers a shadow is modeled as: each one a little
// larger and as faint, so that together they fade out like a blur.
const shadowLayers = 20

// darkShadowBoost makes shadows darker on dark themes.
const darkShadowBoost = 2.0

// Dialogs on a dark theme stand out of it by more than their shadow: the
// scrim under them is darker, and their card lighter.
const (
	darkScrimOpacity = token.OpacityLevel(0.32)
	// darkDialogLift is how far the dialog's color goes from the surface's
	// to the color of its text.
	darkDialogLift = 0.07
)

// isDark reports whether the theme's surface is dark.
func isDark(gtx layout.Context) bool {
	surface := scheme(gtx).Surface.Color
	return int(surface.R)+int(surface.G)+int(surface.B) < 3*128
}

// scrimOpacity is how dark the scrim under a dialog is.
func scrimOpacity(gtx layout.Context) token.OpacityLevel {
	if isDark(gtx) {
		return darkScrimOpacity
	}
	return token.OpacityLevel8
}

// dialogSurface is the color of a dialog's card.
func dialogSurface(gtx layout.Context) token.MatColor {
	sc := scheme(gtx)
	if isDark(gtx) {
		return token.LerpColor(sc.Surface.Color, sc.Surface.OnColor, darkDialogLift)
	}
	return sc.Surface.Color
}

// shadowStyle is the shadow of a dialog or a menu, which sets it off from
// the scrim or the content under it.
type shadowStyle struct {
	// blur is how far the shadow reaches beyond the shape, dy how far it
	// is shifted down, as if the light were above.
	blur, dy unit.Dp
	// alpha is how dark it is next to the shape, from 0 to 1.
	alpha float32
}

var (
	// dialogShadow is the shadow of a dialog over its scrim.
	dialogShadow = shadowStyle{blur: 28, dy: 8, alpha: .42}
	// menuShadow is the shadow of a context menu or a picker over the page.
	menuShadow = shadowStyle{blur: 18, dy: 5, alpha: .36}
	// composerShadowStyle is the shadow of a floating composer's plates over
	// the history, softer than a menu's: they lie along its bottom edge.
	composerShadowStyle = shadowStyle{blur: 14, dy: 3, alpha: .3}
)

// shadowIntensityKey keeps the intensity of shadows in the frame's Values,
// in percent of the full ones: the window sets it from the preferences.
const shadowIntensityKey = "komarugram/ui.shadowIntensity"

// withShadowIntensity makes percent the intensity of the shadows drawn in
// the frame of gtx. A whole number under 256 is not copied to the heap when
// it is put into Values.
func withShadowIntensity(gtx layout.Context, percent int) {
	if gtx.Values != nil {
		gtx.Values[shadowIntensityKey] = percent
	}
}

// shadowIntensity is the intensity of the frame's shadows, from 0 to 2, 1
// being the full one.
func shadowIntensity(gtx layout.Context) float32 {
	if percent, ok := gtx.Values[shadowIntensityKey].(int); ok {
		return float32(percent) / 100
	}
	return 1
}

// drawShadow draws the shadow of a rounded rectangle, under what is drawn
// next. It leaves out nothing: the shape itself is covered by the content.
//
// A shadow of layers, one fill each over the whole shape, took 20 times the
// frame without it on a rasterizer without a GPU, and so did rings between
// them, stroked: it is the number of clipped fills that costs, not their
// area. So the shadow is a picture, made once for a radius, a blur and a
// color, and drawn in nine pieces whatever the size of the shape: the
// corners as they are, the sides and the middle stretched.
func drawShadow(gtx layout.Context, rect image.Rectangle, radius int, style shadowStyle) {
	blur := gtx.Dp(style.blur)
	rect = rect.Add(image.Pt(0, gtx.Dp(style.dy)))
	intensity := shadowIntensity(gtx)
	if intensity <= 0 || blur <= 0 || rect.Dx() < 2*radius || rect.Dy() < 2*radius {
		return
	}
	sc := scheme(gtx)
	alpha := style.alpha * intensity
	if isDark(gtx) {
		// Black on a dark surface needs more to be seen.
		alpha = min(alpha*darkShadowBoost, .9)
	}
	key := shadowKey{radius: radius, blur: blur, color: sc.Shadow.SetOpacity(token.OpacityLevel(alpha)).AsNRGBA()}
	picture := shadowPicture(key)
	// The corner is the shape's own corner and the blur around it.
	corner := radius + blur
	area := image.Rect(rect.Min.X-blur, rect.Min.Y-blur, rect.Max.X+blur, rect.Max.Y+blur)
	xs := [4]int{area.Min.X, area.Min.X + corner, area.Max.X - corner, area.Max.X}
	ys := [4]int{area.Min.Y, area.Min.Y + corner, area.Max.Y - corner, area.Max.Y}
	// A shape round end to end, as a capsule, has no straight piece for the
	// middle: the corners meet half way, a pixel or two smaller than in the
	// picture, which does not show.
	if xs[1] > xs[2] {
		xs[1] = (area.Min.X + area.Max.X) / 2
		xs[2] = xs[1]
	}
	if ys[1] > ys[2] {
		ys[1] = (area.Min.Y + area.Max.Y) / 2
		ys[2] = ys[1]
	}
	// The same cuts in the picture, the middle of it two pixels wide.
	size := 2*corner + shadowMiddle
	us := [4]int{0, corner, corner + shadowMiddle, size}
	for row := range 3 {
		for col := range 3 {
			dst := image.Rect(xs[col], ys[row], xs[col+1], ys[row+1])
			src := image.Rect(us[col], us[row], us[col+1], us[row+1])
			if dst.Empty() {
				continue
			}
			if row == 1 && col == 1 {
				// The middle is one color all over.
				paint.FillShape(gtx.Ops, shadowColor(key.color, shadowLayers), clip.Rect(dst).Op())
				continue
			}
			stack := clip.Rect(dst).Push(gtx.Ops)
			// Takes src to dst: the picture drawn with src's corner at
			// dst's, scaled by how much dst is larger.
			scale := f32.Pt(float32(dst.Dx())/float32(src.Dx()), float32(dst.Dy())/float32(src.Dy()))
			origin := f32.Pt(float32(src.Min.X), float32(src.Min.Y))
			at := op.Affine(f32.AffineId().Offset(origin.Mul(-1)).Scale(f32.Point{}, scale).Offset(layout.FPt(dst.Min))).Push(gtx.Ops)
			picture.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			at.Pop()
			stack.Pop()
		}
	}
}

// shadowMiddle is the width of the part of the shadow's picture that is
// stretched, in pixels.
const shadowMiddle = 2

// shadowKey is what a shadow's picture is made for.
type shadowKey struct {
	radius, blur int
	color        color.NRGBA
}

// shadowPictures keeps the pictures made, for every window to use: the few
// that there are, as the radius, the blur and the color change with the
// scale and the theme only.
var shadowPictures struct {
	sync.Mutex
	made map[shadowKey]paint.ImageOp
}

// shadowPicture returns the shadow of a shape the size of its corners.
func shadowPicture(key shadowKey) paint.ImageOp {
	shadowPictures.Lock()
	defer shadowPictures.Unlock()
	if picture, ok := shadowPictures.made[key]; ok {
		return picture
	}
	if len(shadowPictures.made) >= 16 {
		clear(shadowPictures.made)
	}
	if shadowPictures.made == nil {
		shadowPictures.made = map[shadowKey]paint.ImageOp{}
	}
	picture := paint.NewImageOp(makeShadow(key))
	shadowPictures.made[key] = picture
	return picture
}

// shadowColor is the color that layers of a shadow, each with its share of
// the alpha of color, add up to over what is under them: n of them cover it
// with the rest of what each leaves.
func shadowColor(c color.NRGBA, layers float64) color.NRGBA {
	share := float64(c.A) / 255 / shadowLayers
	c.A = uint8(255*(1-math.Pow(1-share, layers)) + .5)
	return c
}

// makeShadow draws the shadow of a rounded square that is two corners and
// shadowMiddle wide, centered in its picture. A pixel is as dark as the
// layers over it add up to: n of them, each with its share of the alpha,
// cover what is under them with the rest. Which are over a pixel depends on
// how far it is from the shape: all of them next to it, none at the edge of
// the blur, and the squared spacing between them puts more near the shape,
// where the fall-off is steepest.
func makeShadow(key shadowKey) *image.RGBA {
	corner := key.radius + key.blur
	size := 2*corner + shadowMiddle
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// The shape is a square of half-width half; the signed distance to a
	// rounded square is the one to its inner square, less the radius.
	center := float64(size) / 2
	half := float64(key.radius) + shadowMiddle/2
	inner := half - float64(key.radius)
	for y := range size {
		for x := range size {
			qx := math.Abs(float64(x)+.5-center) - inner
			qy := math.Abs(float64(y)+.5-center) - inner
			dist := math.Hypot(max(qx, 0), max(qy, 0)) + min(max(qx, qy), 0) - float64(key.radius)
			layers := float64(shadowLayers)
			if dist > 0 {
				layers *= 1 - math.Sqrt(dist/float64(key.blur))
			}
			if layers <= 0 {
				continue
			}
			c := shadowColor(key.color, layers)
			// Premultiplied.
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(uint16(c.R) * uint16(c.A) / 255),
				G: uint8(uint16(c.G) * uint16(c.A) / 255),
				B: uint8(uint16(c.B) * uint16(c.A) / 255),
				A: c.A,
			})
		}
	}
	return img
}
