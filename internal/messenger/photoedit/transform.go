// SPDX-License-Identifier: Unlicense OR MIT

// Package photoedit is the editor of photos and videos without its interface:
// what was done to a picture, in the picture's own coordinates, and how it is
// drawn over it. It knows neither Gio nor FFmpeg: the editor shows what Flatten
// and Preview draw, a photo is saved from Flatten, and a video takes the layers
// of Layers, which FFmpeg lays over its pictures by the same arithmetic.
package photoedit

import (
	"image"
	"math"
)

// Point is a place in the picture's pixels, or in those of what it is shown
// or drawn as.
type Point struct{ X, Y float64 }

// Add is p moved by q.
func (p Point) Add(q Point) Point { return Point{p.X + q.X, p.Y + q.Y} }

// Sub is p moved back by q.
func (p Point) Sub(q Point) Point { return Point{p.X - q.X, p.Y - q.Y} }

// Mul is p scaled by k.
func (p Point) Mul(k float64) Point { return Point{p.X * k, p.Y * k} }

// Len is how far p is from the origin.
func (p Point) Len() float64 { return math.Hypot(p.X, p.Y) }

// rotated is p turned clockwise by degrees, y going down.
func (p Point) rotated(degrees float64) Point {
	if degrees == 0 {
		return p
	}
	s, c := math.Sincos(degrees * math.Pi / 180)
	return Point{p.X*c - p.Y*s, p.X*s + p.Y*c}
}

// Transform is what the picture becomes around what is drawn on it: cut to
// Crop, mirrored, and turned, in this order, as Telegram Desktop's editor does.
type Transform struct {
	// Crop is the part kept, in the picture's pixels.
	Crop image.Rectangle
	// Angle is the turn clockwise: 0, 90, 180 or 270.
	Angle int
	// Flip mirrors it left to right, before the turn.
	Flip bool
}

// Whole is the transform with all of a picture of size kept.
func (t Transform) Whole(size image.Point) Transform {
	t.Crop = image.Rectangle{Max: size}
	return t
}

// Size is the size of what comes out.
func (t Transform) Size() image.Point {
	s := t.Crop.Size()
	if t.Angle%180 != 0 {
		return image.Pt(s.Y, s.X)
	}
	return s
}

// Identity reports whether a picture of size stays as it is.
func (t Transform) Identity(size image.Point) bool {
	return t.Crop == image.Rectangle{Max: size} && t.Angle == 0 && !t.Flip
}

// Map is where p of the picture comes out.
func (t Transform) Map(p Point) Point {
	w, h := float64(t.Crop.Dx()), float64(t.Crop.Dy())
	x, y := p.X-float64(t.Crop.Min.X), p.Y-float64(t.Crop.Min.Y)
	if t.Flip {
		x = w - x
	}
	switch t.Angle {
	case 90:
		return Point{h - y, x}
	case 180:
		return Point{w - x, h - y}
	case 270:
		return Point{y, w - x}
	}
	return Point{x, y}
}

// Unmap is the place of the picture that comes out at p.
func (t Transform) Unmap(p Point) Point {
	w, h := float64(t.Crop.Dx()), float64(t.Crop.Dy())
	var x, y float64
	switch t.Angle {
	case 90:
		x, y = p.Y, h-p.X
	case 180:
		x, y = w-p.X, h-p.Y
	case 270:
		x, y = w-p.Y, p.X
	default:
		x, y = p.X, p.Y
	}
	if t.Flip {
		x = w - x
	}
	return Point{x + float64(t.Crop.Min.X), y + float64(t.Crop.Min.Y)}
}

// Upright is the turn of an element placed now so that it comes out as drawn:
// a thing drawn while the picture is shown turned does not come out turned.
func (t Transform) Upright() (rotation float64, flipped bool) {
	if t.Flip {
		return float64(t.Angle), true
	}
	return float64(-t.Angle), false
}

// Even is the transform with a crop H.264's 4:2:0 pictures can have: its
// corners on even pixels, and at least 2 pixels each way.
func (t Transform) Even() Transform {
	c := t.Crop
	c.Min.X, c.Min.Y = c.Min.X&^1, c.Min.Y&^1
	w, h := max(2, c.Max.X-c.Min.X), max(2, c.Max.Y-c.Min.Y)
	c.Max.X, c.Max.Y = c.Min.X+w&^1, c.Min.Y+h&^1
	t.Crop = c
	return t
}

// Apply is src, the picture, as the transform makes it.
func (t Transform) Apply(src *image.RGBA) *image.RGBA {
	size := t.Size()
	out := image.NewRGBA(image.Rectangle{Max: size})
	for y := range size.Y {
		row := out.Pix[y*out.Stride:]
		for x := range size.X {
			p := t.Unmap(Point{float64(x) + .5, float64(y) + .5})
			sx, sy := int(math.Floor(p.X)), int(math.Floor(p.Y))
			if !(image.Point{X: sx, Y: sy}).In(src.Rect) {
				continue
			}
			copy(row[x*4:x*4+4], src.Pix[src.PixOffset(sx, sy):])
		}
	}
	return out
}
