// SPDX-License-Identifier: Unlicense OR MIT

package ratex

import (
	"image"
	"image/color"

	"github.com/go-text/typesetting/font/opentype"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// Fallback draws a character no KaTeX font has, as Cyrillic in \text,
// with the host's own text font: size px high, its baseline's origin at
// origin, in col.
type Fallback func(gtx layout.Context, r rune, size float32, origin f32.Point, col color.NRGBA)

// Size is how big l is drawn em px to an em, and its baseline from the top.
func (l *List) Size(em float32) (size image.Point, baseline int) {
	return image.Pt(ceil(l.Width*em), ceil((l.Height+l.Depth)*em)), ceil(l.Height * em)
}

func ceil(v float32) int {
	i := int(v)
	if float32(i) < v {
		i++
	}
	return i
}

// Draw draws l with the top left of its box at the origin, em px to an em,
// as RaTeX's own renderer does: what has no colour of its own in fg.
// Characters no KaTeX font has go to fallback, or are left out without
// one.
func (l *List) Draw(gtx layout.Context, em float32, fg color.NRGBA, fallback Fallback) {
	colorOf := func(c *Color) color.NRGBA {
		if !c.Own() {
			return fg
		}
		return color.NRGBA{R: unit8(c.R), G: unit8(c.G), B: unit8(c.B), A: unit8(c.A)}
	}
	for _, it := range l.Items {
		col := colorOf(it.Color)
		switch it.Type {
		case GlyphPath:
			origin := f32.Pt(it.X*em, it.Y*em)
			size := em * it.Scale
			o := glyph(it.Font, it.CharCode)
			if o == nil {
				if fallback != nil {
					fallback(gtx, it.CharCode, size, origin, col)
				}
				continue
			}
			drawGlyph(gtx, o, origin, size/o.upem, col)
		case Line:
			t := max(it.Thickness*em, 1)
			x, y, w := it.X*em, it.Y*em, it.Width*em
			if !it.Dashed {
				fillRect(gtx, x, y-t/2, x+w, y+t/2, col)
				continue
			}
			dash := max(4*t, 2)
			for at := x; at < x+w; at += 2 * dash {
				fillRect(gtx, at, y-t/2, min(at+dash, x+w), y+t/2, col)
			}
		case Rect:
			x, y := it.X*em, it.Y*em
			fillRect(gtx, x, y, x+it.Width*em, y+it.Height*em, col)
		case Path:
			drawPath(gtx, it, em, col)
		}
	}
}

func unit8(v float32) uint8 {
	return uint8(min(max(v, 0), 1)*255 + .5)
}

// fillRect fills the rectangle from x0, y0 to x1, y1.
func fillRect(gtx layout.Context, x0, y0, x1, y1 float32, col color.NRGBA) {
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(x0, y0))
	p.LineTo(f32.Pt(x1, y0))
	p.LineTo(f32.Pt(x1, y1))
	p.LineTo(f32.Pt(x0, y1))
	p.Close()
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: p.End()}.Op())
}

// drawGlyph fills outline o, its origin at origin, scale px to a unit of
// its font, whose y is up.
func drawGlyph(gtx layout.Context, o *outline, origin f32.Point, scale float32, col color.NRGBA) {
	pt := func(s opentype.SegmentPoint) f32.Point {
		return f32.Pt(origin.X+s.X*scale, origin.Y-s.Y*scale)
	}
	var p clip.Path
	p.Begin(gtx.Ops)
	open := false
	for _, s := range o.segments {
		switch s.Op {
		case opentype.SegmentOpMoveTo:
			if open {
				p.Close()
			}
			p.MoveTo(pt(s.Args[0]))
			open = true
		case opentype.SegmentOpLineTo:
			p.LineTo(pt(s.Args[0]))
		case opentype.SegmentOpQuadTo:
			p.QuadTo(pt(s.Args[0]), pt(s.Args[1]))
		case opentype.SegmentOpCubeTo:
			p.CubeTo(pt(s.Args[0]), pt(s.Args[1]), pt(s.Args[2]))
		}
	}
	if open {
		p.Close()
	}
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: p.End()}.Op())
}

// drawPath draws a path: each of its parts filled on its own, as RaTeX
// fills them (the parts of a stretched arrow wind both ways, and would
// cancel out together), or else stroked.
func drawPath(gtx layout.Context, it Item, em float32, col color.NRGBA) {
	pt := func(x, y float32) f32.Point { return f32.Pt((it.X+x)*em, (it.Y+y)*em) }
	var p clip.Path
	started := false
	flush := func() {
		if !started {
			return
		}
		spec := p.End()
		if it.Fill {
			paint.FillShape(gtx.Ops, col, clip.Outline{Path: spec}.Op())
		} else {
			paint.FillShape(gtx.Ops, col, clip.Stroke{Path: spec, Width: 1.5 * gtx.Metric.PxPerDp}.Op())
		}
		started = false
	}
	for _, c := range it.Commands {
		if c.Type == MoveTo || !started {
			flush()
			p.Begin(gtx.Ops)
			started = true
			if c.Type != MoveTo {
				p.MoveTo(pt(0, 0))
			}
		}
		switch c.Type {
		case MoveTo:
			p.MoveTo(pt(c.X, c.Y))
		case LineTo:
			p.LineTo(pt(c.X, c.Y))
		case QuadTo:
			p.QuadTo(pt(c.X1, c.Y1), pt(c.X, c.Y))
		case CubicTo:
			p.CubeTo(pt(c.X1, c.Y1), pt(c.X2, c.Y2), pt(c.X, c.Y))
		case Close:
			p.Close()
		}
	}
	flush()
}
