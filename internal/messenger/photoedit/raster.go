// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import (
	"image"
	"math"

	"golang.org/x/image/vector"
)

// Canvas is the grid of pixels something is drawn on: Rect of the picture,
// Scale times its pixels. A photo saved is Scale 1 and all of it; the editor's
// view is the part cropped at the size it is shown.
type Canvas struct {
	Scale float64
	Rect  image.Rectangle
}

// CanvasOf is the canvas of part of a picture, whose pixels it has scale of.
func CanvasOf(part image.Rectangle, scale float64) Canvas {
	return Canvas{Scale: scale, Rect: part}
}

// Size is the canvas's in pixels.
func (c Canvas) Size() image.Point {
	return image.Pt(max(1, int(math.Round(float64(c.Rect.Dx())*c.Scale))), max(1, int(math.Round(float64(c.Rect.Dy())*c.Scale))))
}

// To is where p of the picture is on the canvas.
func (c Canvas) To(p Point) Point {
	return Point{(p.X - float64(c.Rect.Min.X)) * c.Scale, (p.Y - float64(c.Rect.Min.Y)) * c.Scale}
}

// segments is how many lines a circle of radius r is drawn in.
func segments(r float64) int {
	return min(64, max(8, int(math.Ceil(r*1.2))))
}

// outline gathers polygons of one orientation, whose union vector.Rasterizer
// covers: it adds what overlaps and clamps it, so that opposite ones would
// cancel out.
type outline struct {
	polygons [][]Point
}

// add puts polygon in, turned the same way as the others.
func (o *outline) add(polygon []Point) {
	area := 0.
	for i, p := range polygon {
		q := polygon[(i+1)%len(polygon)]
		area += p.X*q.Y - q.X*p.Y
	}
	if area < 0 {
		polygon = append([]Point(nil), polygon...)
		for i, j := 0, len(polygon)-1; i < j; i, j = i+1, j-1 {
			polygon[i], polygon[j] = polygon[j], polygon[i]
		}
	}
	o.polygons = append(o.polygons, polygon)
}

// disc adds a circle.
func (o *outline) disc(c Point, r float64) {
	n := segments(r)
	polygon := make([]Point, n)
	for i := range polygon {
		s, k := math.Sincos(2 * math.Pi * float64(i) / float64(n))
		polygon[i] = Point{c.X + r*k, c.Y + r*s}
	}
	o.add(polygon)
}

// line adds a line of width with round caps and joins through points.
func (o *outline) line(points []Point, width float64) {
	r := width / 2
	if len(points) == 0 {
		return
	}
	o.disc(points[0], r)
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		d := b.Sub(a)
		l := d.Len()
		if l == 0 {
			continue
		}
		n := Point{-d.Y / l * r, d.X / l * r}
		o.add([]Point{a.Add(n), b.Add(n), b.Sub(n), a.Sub(n)})
		// A join that hardly turns needs no disc: most of a smoothed line.
		if i+1 == len(points) || sharp(a, b, points[i+1], r) {
			o.disc(b, r)
		}
	}
}

// sharp reports whether the line turns at b enough to show a notch.
func sharp(a, b, c Point, r float64) bool {
	d, e := b.Sub(a), c.Sub(b)
	cross, dot := d.X*e.Y-d.Y*e.X, d.X*e.X+d.Y*e.Y
	// The gap at the outer side of the turn is about r times its angle.
	return dot <= 0 || math.Abs(math.Atan2(cross, dot))*r > .25
}

// cover is how much of each pixel of the canvas the outline covers, within
// the rectangle that holds it.
func (o *outline) cover(c Canvas) *image.Alpha {
	bounds := o.bounds()
	size := c.Size()
	bounds = bounds.Intersect(image.Rectangle{Max: size})
	if bounds.Empty() {
		return nil
	}
	cov := image.NewAlpha(bounds)
	r := vector.NewRasterizer(bounds.Dx(), bounds.Dy())
	ox, oy := float32(bounds.Min.X), float32(bounds.Min.Y)
	for _, polygon := range o.polygons {
		if len(polygon) < 3 {
			continue
		}
		r.MoveTo(float32(polygon[0].X)-ox, float32(polygon[0].Y)-oy)
		for _, p := range polygon[1:] {
			r.LineTo(float32(p.X)-ox, float32(p.Y)-oy)
		}
		r.ClosePath()
	}
	r.Draw(cov, bounds, image.Opaque, image.Point{})
	return cov
}

// bounds is the rectangle of whole pixels that holds all the polygons.
func (o *outline) bounds() image.Rectangle {
	lo, hi := Point{math.Inf(1), math.Inf(1)}, Point{math.Inf(-1), math.Inf(-1)}
	for _, polygon := range o.polygons {
		for _, p := range polygon {
			if math.IsNaN(p.X) || math.IsNaN(p.Y) {
				continue
			}
			lo.X, lo.Y = min(lo.X, p.X), min(lo.Y, p.Y)
			hi.X, hi.Y = max(hi.X, p.X), max(hi.Y, p.Y)
		}
	}
	if lo.X > hi.X {
		return image.Rectangle{}
	}
	// Within what int holds: a far point is cut by the canvas anyway.
	const far = 1 << 24
	clamp := func(v float64) int { return int(max(-far, min(far, v))) }
	return image.Rect(clamp(math.Floor(lo.X)), clamp(math.Floor(lo.Y)), clamp(math.Ceil(hi.X)), clamp(math.Ceil(hi.Y)))
}

// smooth is points through the midpoints between them, bent toward each, as
// Telegram Desktop draws a stroke: the hand's line without its corners.
func smooth(points []Point) []Point {
	if len(points) < 3 {
		return points
	}
	out := make([]Point, 0, len(points)*2)
	out = append(out, points[0])
	for i := 1; i < len(points)-1; i++ {
		c := points[i]
		from, to := points[i-1].Add(c).Mul(.5), c.Add(points[i+1]).Mul(.5)
		if i == 1 {
			from = points[0]
		}
		n := min(8, max(2, int((c.Sub(from).Len()+to.Sub(c).Len())/4)))
		for k := 1; k <= n; k++ {
			t := float64(k) / float64(n)
			a, b := from.Mul(1-t).Add(c.Mul(t)), c.Mul(1-t).Add(to.Mul(t))
			out = append(out, a.Mul(1-t).Add(b.Mul(t)))
		}
	}
	return append(out, points[len(points)-1])
}

// strokeOutline is a stroke on a canvas.
func strokeOutline(s *Stroke, c Canvas) *outline {
	points := make([]Point, len(s.Points))
	for i, p := range s.Points {
		points[i] = c.To(p)
	}
	line := smooth(points)
	width := max(s.lineWidth()*c.Scale, .5)
	o := new(outline)
	o.line(line, width)
	if s.Tool == Arrow {
		arrowHead(o, line, s.Width*c.Scale, width)
	}
	return o
}

// arrowHead adds the head of an arrow, of a brush of size, at the end of a line
// long enough for one.
func arrowHead(o *outline, line []Point, size, width float64) {
	length := size * arrowHeadLength
	tip := line[len(line)-1]
	// The way the line came, a head's length back.
	from, walked := tip, 0.
	for i := len(line) - 2; i >= 0 && walked < length; i-- {
		walked += line[i+1].Sub(line[i]).Len()
		from = line[i]
	}
	if walked < size*arrowHeadMinimum {
		return
	}
	d := tip.Sub(from)
	back := d.Mul(-length / d.Len())
	o.line([]Point{tip.Add(back.rotated(arrowHeadAngle)), tip, tip.Add(back.rotated(-arrowHeadAngle))}, width)
}

// Figures' proportions, Telegram Desktop's (scene_item_shape.cpp).
const (
	starInner      = .4316
	starBoxWidth   = 1.902113
	starBoxHeight  = 1.809017
	shapeRounding  = .234
	bubbleBody     = 2. / 2.25
	bubbleTail     = .75
	shapeArrowRoot = .16
	shapeArrowHead = 30.
)

// StarAspect is a star's height to its width, which its box keeps.
const StarAspect = starBoxHeight / starBoxWidth

// roundRect is a rectangle of half sides w and h with corners of radius r.
func roundRect(w, h, r float64) []Point {
	r = min(r, w, h)
	n := max(2, segments(r)/4)
	var out []Point
	corners := [4]Point{{w - r, -h + r}, {w - r, h - r}, {-w + r, h - r}, {-w + r, -h + r}}
	for i, c := range corners {
		for k := range n + 1 {
			angle := (float64(i-1) + float64(k)/float64(n)) * math.Pi / 2
			s, k := math.Sincos(angle)
			out = append(out, Point{c.X + r*k, c.Y + r*s})
		}
	}
	return out
}

// shapeOutlines are the closed lines and the open ones of a shape in its box,
// around its center.
func shapeLines(sh *Shape, w, h float64) (closed [][]Point, open [][]Point) {
	hw, hh := w/2, h/2
	switch sh.Type {
	case ShapeCircle:
		n := segments(max(hw, hh)) * 2
		polygon := make([]Point, n)
		for i := range polygon {
			s, k := math.Sincos(2 * math.Pi * float64(i) / float64(n))
			polygon[i] = Point{hw * k, hh * s}
		}
		closed = append(closed, polygon)
	case ShapeRectangle:
		closed = append(closed, roundRect(hw, hh, min(w, h)*shapeRounding))
	case ShapeStar:
		outer := min(w/starBoxWidth, h/starBoxHeight)
		center := Point{0, outer - starBoxHeight*outer/2}
		polygon := make([]Point, 10)
		for i := range polygon {
			r := outer
			if i%2 == 1 {
				r = outer * starInner
			}
			s, k := math.Sincos(float64(i)*math.Pi/5 - math.Pi/2)
			polygon[i] = Point{center.X + r*k, center.Y + r*s}
		}
		closed = append(closed, polygon)
	case ShapeBubble:
		body := h * bubbleBody
		rect := roundRect(hw, body/2, min(w, body)*shapeRounding)
		polygon := make([]Point, 0, len(rect)+3)
		// The tail goes down at the left, in the body's bottom edge.
		base := w * .08
		root := -w * .5 * bubbleTail / 2
		for i, p := range rect {
			p.Y -= (h - body) / 2
			polygon = append(polygon, p)
			if q := rect[(i+1)%len(rect)]; p.Y > 0 && q.X < p.X && math.Abs(q.Y-rect[i].Y) < 1e-9 {
				polygon = append(polygon, Point{root + base, p.Y}, Point{root - base, hh}, Point{root - base, p.Y})
			}
		}
		closed = append(closed, polygon)
	case ShapeArrow:
		head := max(w*shapeArrowRoot, sh.Width*2.5)
		tip := Point{hw, 0}
		open = append(open, []Point{{-hw, 0}, tip})
		left := Point{-head, 0}.rotated(shapeArrowHead).Add(tip)
		right := Point{-head, 0}.rotated(-shapeArrowHead).Add(tip)
		if sh.Filled {
			closed = append(closed, []Point{tip, left, right})
		} else {
			open = append(open, []Point{left, tip, right})
		}
	}
	return closed, open
}

// shapeOutline is an element's shape on a canvas.
func shapeOutline(e *Element, c Canvas) *outline {
	closed, open := shapeLines(e.Shape, e.Width, e.Height)
	at := func(p Point) Point {
		if e.Flipped {
			p.X = -p.X
		}
		return c.To(p.rotated(e.Rotation).Add(e.Center))
	}
	mapped := func(line []Point) []Point {
		out := make([]Point, len(line))
		for i, p := range line {
			out[i] = at(p)
		}
		return out
	}
	width := max(e.Shape.Width*c.Scale, .5)
	o := new(outline)
	for _, polygon := range closed {
		polygon = mapped(polygon)
		if e.Shape.Filled {
			o.add(polygon)
		} else {
			o.line(append(polygon, polygon[0]), width)
		}
	}
	for _, line := range open {
		o.line(mapped(line), width)
	}
	return o
}

// erase takes what cut covers out of cov.
func erase(cov, cut *image.Alpha) {
	if cov == nil || cut == nil {
		return
	}
	r := cov.Rect.Intersect(cut.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		a := cov.Pix[cov.PixOffset(r.Min.X, y):]
		b := cut.Pix[cut.PixOffset(r.Min.X, y):]
		for x := range r.Dx() {
			a[x] = uint8(uint32(a[x]) * (255 - uint32(b[x])) / 255)
		}
	}
}
