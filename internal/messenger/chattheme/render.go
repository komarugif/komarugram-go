// SPDX-License-Identifier: Unlicense OR MIT
// Package chattheme decodes and paints Telegram's public theme formats.
package chattheme

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"math"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/resample"
	"komarugram/pkg/unpack"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	_ "golang.org/x/image/webp"
)

const MaxBytes = 16 << 20

// MaxSide bounds the side of a rendered wallpaper; a larger chat draws it
// scaled up.
const MaxSide = 2048

func RGB(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

// freeformPoints are where the colors of a freeform gradient sit, in a
// unit square; color i sits at point (phase + 2i) mod 8, and a phase
// moves each to the next point, as Telegram's gradients turn when a
// message is sent.
var freeformPoints = [8][2]float64{{.80, .10}, {.60, .20}, {.35, .25}, {.25, .60}, {.20, .90}, {.40, .80}, {.65, .75}, {.75, .40}}

// Gradient uses a linear fill for two colors and a freeform gradient for
// three or four. phase moves the freeform colors to their next points.
func Gradient(colors []uint32, w, h, rotation int, phase float64) *image.RGBA {
	w = max(1, w)
	h = max(1, h)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if len(colors) == 0 {
		return dst
	}
	colors = colors[:min(4, len(colors))]
	angle := float64(rotation) * math.Pi / 180
	dx, dy := -math.Sin(angle), math.Cos(angle)
	span := math.Abs(dx) + math.Abs(dy)
	var points [4][2]float64
	whole, frac := math.Floor(phase), phase-math.Floor(phase)
	for i := range points {
		a := freeformPoints[(int(whole)+2*i)%8]
		b := freeformPoints[(int(whole)+2*i+1)%8]
		points[i] = [2]float64{a[0] + (b[0]-a[0])*frac, a[1] + (b[1]-a[1])*frac}
	}
	var rgb [4][3]float64
	for i, v := range colors {
		c := RGB(v)
		rgb[i] = [3]float64{float64(c.R), float64(c.G), float64(c.B)}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			nx, ny := (float64(x)+.5)/float64(w), (float64(y)+.5)/float64(h)
			var weights [4]float64
			switch len(colors) {
			case 1:
				weights[0] = 1
			case 2:
				// The colors are pure at the edges.
				ex, ey := float64(x)/float64(max(1, w-1)), float64(y)/float64(max(1, h-1))
				t := max(0, min(1, .5+((ex-.5)*dx+(ey-.5)*dy)/span))
				weights[0], weights[1] = 1-t, t
			default:
				// The colors swirl a little around the middle.
				cx, cy := nx-.5, ny-.5
				theta := math.Pow(math.Hypot(cx, cy)*.35, 2) * 6.4
				s, c := math.Sincos(theta)
				px := max(0, min(1, .5+cx*c-cy*s))
				py := max(0, min(1, .5+cx*s+cy*c))
				sum := 0.
				for i := range colors {
					d := max(0, .9-math.Hypot(px-points[i][0], py-points[i][1]))
					d *= d
					weights[i] = d * d
					sum += weights[i]
				}
				if sum == 0 {
					weights[0], sum = 1, 1
				}
				for i := range colors {
					weights[i] /= sum
				}
			}
			var r, g, b float64
			for i := range colors {
				r += rgb[i][0] * weights[i]
				g += rgb[i][1] * weights[i]
				b += rgb[i][2] * weights[i]
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r + .5), uint8(g + .5), uint8(b + .5), 255})
		}
	}
	return dst
}

// gzipped unpacks a TGV pattern: a gzipped SVG.
func gzipped(data []byte) ([]byte, error) {
	if len(data) <= 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data, nil
	}
	data, err := unpack.Gzip(data, MaxBytes)
	if errors.Is(err, unpack.ErrTooLarge) {
		return nil, errors.New("expanded wallpaper exceeds 16 MiB")
	}
	return data, err
}

func isSVG(data []byte) bool { return bytes.Contains(data[:min(len(data), 1024)], []byte("<svg")) }

func parseSVG(data []byte) (*oksvg.SvgIcon, error) {
	// oksvg handles geometry only; scripts and external image fetching are absent.
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if icon.ViewBox.W <= 0 || icon.ViewBox.H <= 0 || math.IsInf(icon.ViewBox.W, 0) || math.IsInf(icon.ViewBox.H, 0) || math.IsNaN(icon.ViewBox.W) || math.IsNaN(icon.ViewBox.H) {
		return nil, errors.New("invalid wallpaper dimensions")
	}
	return icon, nil
}

// coverTarget places an SVG to cover w×h, cut in the middle as a picture is.
func coverTarget(icon *oksvg.SvgIcon, w, h int) {
	scale := max(float64(w)/icon.ViewBox.W, float64(h)/icon.ViewBox.H)
	sw, sh := icon.ViewBox.W*scale, icon.ViewBox.H*scale
	icon.SetTarget((float64(w)-sw)/2, (float64(h)-sh)/2, sw, sh)
}

// rasterize draws an SVG in its colors to cover w×h.
func rasterize(icon *oksvg.SvgIcon, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	coverTarget(icon, w, h)
	icon.Draw(rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, dst, dst.Bounds())), 1)
	return dst
}

// rasterizeMask draws the shapes of an SVG pattern to cover w×h, as alpha.
func rasterizeMask(icon *oksvg.SvgIcon, w, h int) *image.Alpha {
	dst := image.NewAlpha(image.Rect(0, 0, w, h))
	coverTarget(icon, w, h)
	icon.Draw(rasterx.NewDasher(w, h, newAlphaScanner(dst)), 1)
	return dst
}

// alphaOf is the alpha of im.
func alphaOf(im *image.RGBA) *image.Alpha {
	out := image.NewAlpha(im.Bounds())
	for i := range out.Pix {
		out.Pix[i] = im.Pix[i*4+3]
	}
	return out
}

func Decode(data []byte) (image.Image, error) {
	if len(data) > MaxBytes {
		return nil, errors.New("wallpaper exceeds 16 MiB")
	}
	data, err := gzipped(data)
	if err != nil {
		return nil, err
	}
	if isSVG(data) {
		icon, err := parseSVG(data)
		if err != nil {
			return nil, err
		}
		scale := min(1., 1024/math.Max(icon.ViewBox.W, icon.ViewBox.H))
		return rasterize(icon, max(1, int(icon.ViewBox.W*scale)), max(1, int(icon.ViewBox.H*scale))), nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 32<<20 {
		return nil, errors.New("wallpaper dimensions exceed limit")
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	return im, err
}

// Background is a wallpaper decoded once, to be rendered at the sizes the
// chat takes.
type Background struct {
	colors    []uint32
	rotation  int
	intensity int
	blur      bool
	tile      bool
	// photo is the picture over the colors, blurred already when asked;
	// a pattern is either vector, drawn at each size, or a picture.
	photo   image.Image
	vector  *oksvg.SvgIcon
	pattern image.Image
	// average is the color the wallpaper averages to, RGB.
	average uint32
}

// Prepare decodes a wallpaper and its picture, data, which may be nil.
// A picture that fails leaves the colors, with the error.
func Prepare(w *model.ChatWallpaper, data []byte) (*Background, error) {
	b := &Background{colors: append([]uint32(nil), w.Colors...), rotation: w.Rotation, intensity: max(-100, min(100, w.Intensity)), blur: w.Blur, tile: w.Tile}
	if len(b.colors) == 0 {
		b.colors = []uint32{0xd8e4dc}
		if w.Dark {
			b.colors = []uint32{0x0e1621}
		}
	}
	var err error
	if len(data) > 0 {
		err = b.decode(w, data)
	}
	small := b.Render(image.Pt(24, 32))
	var r, g, bl, n uint64
	for i := 0; i < len(small.Pix); i += 4 {
		r += uint64(small.Pix[i])
		g += uint64(small.Pix[i+1])
		bl += uint64(small.Pix[i+2])
		n++
	}
	b.average = uint32(r/n)<<16 | uint32(g/n)<<8 | uint32(bl/n)
	return b, err
}

func (b *Background) decode(w *model.ChatWallpaper, data []byte) error {
	if len(data) > MaxBytes {
		return errors.New("wallpaper exceeds 16 MiB")
	}
	data, err := gzipped(data)
	if err != nil {
		return err
	}
	if isSVG(data) {
		icon, err := parseSVG(data)
		if err != nil {
			return err
		}
		if w.Pattern {
			b.vector = icon
		} else {
			b.photo = rasterize(icon, min(1024, int(icon.ViewBox.W)), min(1024, int(icon.ViewBox.H)))
		}
		return nil
	}
	im, err := Decode(data)
	if err != nil {
		return err
	}
	switch {
	case w.Pattern:
		b.pattern = im
	case b.blur:
		b.photo = boxBlur(scaleFit(im, 450), 12)
	default:
		b.photo = im
	}
	return nil
}

// Average is the color the wallpaper averages to, RGB: service messages
// over it take their hue from it.
func (b *Background) Average() uint32 { return b.average }

// Render draws the wallpaper at size, bounded by MaxSide.
func (b *Background) Render(size image.Point) *image.RGBA {
	w, h := max(1, size.X), max(1, size.Y)
	if s := max(w, h); s > MaxSide {
		w, h = max(1, w*MaxSide/s), max(1, h*MaxSide/s)
	}
	out := b.base(w, h)
	switch {
	case b.vector != nil || b.pattern != nil:
		var mask *image.Alpha
		if b.vector != nil {
			mask = rasterizeMask(b.vector, w, h)
		} else {
			mask = alphaOf(cover(b.pattern, w, h))
		}
		applyPattern(out, mask, b.intensity)
	case b.photo != nil && b.tile:
		pb := b.photo.Bounds()
		for y := 0; y < h; y += pb.Dy() {
			for x := 0; x < w; x += pb.Dx() {
				draw.Draw(out, image.Rect(x, y, x+pb.Dx(), y+pb.Dy()), b.photo, pb.Min, draw.Over)
			}
		}
	case b.photo != nil:
		draw.Draw(out, out.Bounds(), cover(b.photo, w, h), image.Point{}, draw.Over)
	}
	return out
}

// base is the wallpaper's colors at w×h. A freeform gradient is worked out
// small and scaled, as its colors change slowly.
func (b *Background) base(w, h int) *image.RGBA {
	if len(b.colors) == 1 {
		out := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(out, out.Bounds(), image.NewUniform(RGB(b.colors[0])), image.Point{}, draw.Src)
		return out
	}
	gw, gh := min(w, 60), min(h, 80)
	if len(b.colors) == 2 {
		gw, gh = min(w, 128), min(h, 128)
	}
	small := Gradient(b.colors, gw, gh, b.rotation, 0)
	if gw == w && gh == h {
		return small
	}
	return resample.Resize(small, w, h)
}

// cover scales im to cover w×h, cut in the middle.
func cover(im image.Image, w, h int) *image.RGBA {
	ib := im.Bounds()
	scale := max(float64(w)/float64(ib.Dx()), float64(h)/float64(ib.Dy()))
	// Crop in the source first: a hostile aspect ratio makes no huge image.
	cw, ch := max(1, min(ib.Dx(), int(math.Ceil(float64(w)/scale)))), max(1, min(ib.Dy(), int(math.Ceil(float64(h)/scale))))
	x0, y0 := ib.Min.X+(ib.Dx()-cw)/2, ib.Min.Y+(ib.Dy()-ch)/2
	crop := image.NewRGBA(image.Rect(0, 0, cw, ch))
	draw.Draw(crop, crop.Bounds(), im, image.Pt(x0, y0), draw.Src)
	if cw == w && ch == h {
		return crop
	}
	return resample.Resize(crop, w, h)
}

// applyPattern lays a pattern's shapes, the alpha of mask, over dst. A
// positive intensity darkens the colors under them as a soft light does,
// keeping their hue; a negative one, of dark wallpapers, shows the colors
// only through the shapes, over black.
func applyPattern(dst *image.RGBA, mask *image.Alpha, intensity int) {
	k := float64(intensity) / 100
	// What a channel becomes, by the shape's alpha and the channel.
	var table [256][256]uint8
	for a := range 256 {
		for c := range 256 {
			v := float64(c) / 255
			if k >= 0 {
				o := float64(a) / 255 * k
				v = v*(1-o) + v*v*o
			} else {
				v *= float64(a) / 255 * -k
			}
			table[a][c] = uint8(v*255 + .5)
		}
	}
	b := dst.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := dst.Pix[dst.PixOffset(b.Min.X, y):dst.PixOffset(b.Max.X, y)]
		alpha := mask.Pix[mask.PixOffset(b.Min.X, y):]
		for i := 0; i < len(row); i += 4 {
			t := &table[alpha[i/4]]
			row[i], row[i+1], row[i+2] = t[row[i]], t[row[i+1]], t[row[i+2]]
		}
	}
}

func scaleFit(im image.Image, n int) image.Image {
	b := im.Bounds()
	scale := min(1., float64(n)/float64(max(b.Dx(), b.Dy())))
	// Draw uses the project's streaming resampler, avoiding giant intermediates.
	return resample.Resize(im, max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale)))
}
func boxBlur(src image.Image, radius int) *image.RGBA {
	b := src.Bounds()
	tmp := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	out := image.NewRGBA(tmp.Bounds())
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			var r, g, bb, a, n uint32
			for k := max(0, x-radius); k <= min(b.Dx()-1, x+radius); k++ {
				cr, cg, cb, ca := src.At(b.Min.X+k, b.Min.Y+y).RGBA()
				r += cr
				g += cg
				bb += cb
				a += ca
				n++
			}
			tmp.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bb / n >> 8), uint8(a / n >> 8)})
		}
	}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			var r, g, bb, a, n uint32
			for k := max(0, y-radius); k <= min(b.Dy()-1, y+radius); k++ {
				c := tmp.RGBAAt(x, k)
				r += uint32(c.R)
				g += uint32(c.G)
				bb += uint32(c.B)
				a += uint32(c.A)
				n++
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(bb / n), uint8(a / n)})
		}
	}
	return out
}
