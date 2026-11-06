// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import (
	"image"
	"math"
)

// Grab is which edges of a crop a drag holds; none for moving all of it.
type Grab struct{ Left, Top, Right, Bottom bool }

// GrabAt is what a press at p takes of crop, reach pixels of the picture around
// its edges. ok is false for a press beyond them.
func GrabAt(crop image.Rectangle, p Point, reach float64) (g Grab, ok bool) {
	x0, y0, x1, y1 := float64(crop.Min.X), float64(crop.Min.Y), float64(crop.Max.X), float64(crop.Max.Y)
	if p.X < x0-reach || p.X > x1+reach || p.Y < y0-reach || p.Y > y1+reach {
		return Grab{}, false
	}
	g.Left = math.Abs(p.X-x0) <= reach
	g.Right = !g.Left && math.Abs(p.X-x1) <= reach
	g.Top = math.Abs(p.Y-y0) <= reach
	g.Bottom = !g.Top && math.Abs(p.Y-y1) <= reach
	return g, true
}

// DragCrop is crop start with g dragged by delta, within a picture of size, no
// side under minSide, and with the aspect of width to height unless 0.
func DragCrop(start image.Rectangle, g Grab, delta Point, size image.Point, aspect float64, minSide int) image.Rectangle {
	if g == (Grab{}) {
		dx := max(-start.Min.X, min(size.X-start.Max.X, int(math.Round(delta.X))))
		dy := max(-start.Min.Y, min(size.Y-start.Max.Y, int(math.Round(delta.Y))))
		return start.Add(image.Pt(dx, dy))
	}
	x0, y0, x1, y1 := float64(start.Min.X), float64(start.Min.Y), float64(start.Max.X), float64(start.Max.Y)
	side := float64(minSide)
	if g.Left {
		x0 = max(0, min(x0+delta.X, x1-side))
	}
	if g.Right {
		x1 = min(float64(size.X), max(x1+delta.X, x0+side))
	}
	if g.Top {
		y0 = max(0, min(y0+delta.Y, y1-side))
	}
	if g.Bottom {
		y1 = min(float64(size.Y), max(y1+delta.Y, y0+side))
	}
	if aspect > 0 {
		x0, y0, x1, y1 = keepAspect(x0, y0, x1, y1, g, size, aspect)
	}
	return image.Rect(int(math.Round(x0)), int(math.Round(y0)), int(math.Round(x1)), int(math.Round(y1)))
}

// keepAspect makes a rectangle dragged by g of aspect, around the corner or the
// edge it stays at, as large as it fits.
func keepAspect(x0, y0, x1, y1 float64, g Grab, size image.Point, aspect float64) (float64, float64, float64, float64) {
	w, h := x1-x0, y1-y0
	sideways, upward := g.Left || g.Right, g.Top || g.Bottom
	switch {
	case sideways && upward:
		// The corner follows the side that went further.
		if w/aspect > h {
			h = w / aspect
		} else {
			w = h * aspect
		}
	case sideways:
		h = w / aspect
	default:
		w = h * aspect
	}
	// Where the box may reach from what stays.
	roomX, roomY := float64(size.X), float64(size.Y)
	cx, cy := (x0+x1)/2, (y0+y1)/2
	switch {
	case g.Left:
		roomX = x1
	case g.Right:
		roomX = float64(size.X) - x0
	default:
		roomX = 2 * min(cx, float64(size.X)-cx)
	}
	switch {
	case g.Top:
		roomY = y1
	case g.Bottom:
		roomY = float64(size.Y) - y0
	default:
		roomY = 2 * min(cy, float64(size.Y)-cy)
	}
	if k := min(1, roomX/w, roomY/h); k < 1 {
		w, h = w*k, h*k
	}
	switch {
	case g.Left:
		x0 = x1 - w
	case g.Right:
		x1 = x0 + w
	default:
		x0, x1 = cx-w/2, cx+w/2
	}
	switch {
	case g.Top:
		y0 = y1 - h
	case g.Bottom:
		y1 = y0 + h
	default:
		y0, y1 = cy-h/2, cy+h/2
	}
	return x0, y0, x1, y1
}

// FitAspect is the largest rectangle of aspect, width to height, in the middle
// of r.
func FitAspect(r image.Rectangle, aspect float64) image.Rectangle {
	if aspect <= 0 || r.Empty() {
		return r
	}
	w, h := float64(r.Dx()), float64(r.Dy())
	if w/aspect > h {
		w = h * aspect
	} else {
		h = w / aspect
	}
	x := r.Min.X + (r.Dx()-int(w))/2
	y := r.Min.Y + (r.Dy()-int(h))/2
	return image.Rect(x, y, x+int(w), y+int(h))
}
