// SPDX-License-Identifier: Unlicense OR MIT

package ratex

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/go-text/typesetting/font/opentype"
)

// svgUnits is how many units of an SVG's view box an em is.
const svgUnits = 1000

// SVG is l as an SVG image, as Draw draws it: as wide and high as l in
// ems of the text around it, set on that text's baseline, its glyphs
// outlines, so that it needs no font. What has no colour of its own is
// fg, a CSS colour; "currentColor" takes the text's. Characters no KaTeX
// font has are text in the reader's sans-serif font. title, when not
// empty, is what the image says it shows, as a formula's source.
func (l *List) SVG(fg, title string) string {
	var b strings.Builder
	width, height := l.Width*svgUnits, (l.Height+l.Depth)*svgUnits
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" role="img" width="%sem" height="%sem" viewBox="0 0 %s %s" style="vertical-align:%sem" fill="%s">`,
		num(l.Width, 3), num(l.Height+l.Depth, 3), num(width, 0), num(height, 0), num(-l.Depth, 3), html.EscapeString(fg))
	if title != "" {
		fmt.Fprintf(&b, "<title>%s</title>", html.EscapeString(title))
	}
	colour := func(c *Color) string {
		if !c.Own() {
			return ""
		}
		return fmt.Sprintf(` fill="rgba(%d,%d,%d,%s)"`, unit8(c.R), unit8(c.G), unit8(c.B), num(c.A, 3))
	}
	rect := func(x0, y0, x1, y1 float32, fill string) {
		fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s"%s/>`, num(x0, 1), num(y0, 1), num(x1-x0, 1), num(y1-y0, 1), fill)
	}
	for _, it := range l.Items {
		fill := colour(it.Color)
		switch it.Type {
		case GlyphPath:
			x, y := it.X*svgUnits, it.Y*svgUnits
			size := svgUnits * it.Scale
			o := glyph(it.Font, it.CharCode)
			if o == nil {
				fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="%s" font-family="sans-serif"%s>%s</text>`, num(x, 1), num(y, 1), num(size, 1), fill, html.EscapeString(string(it.CharCode)))
				continue
			}
			b.WriteString(`<path d="`)
			glyphPath(&b, o, x, y, size/o.upem)
			fmt.Fprintf(&b, `"%s/>`, fill)
		case Line:
			t := it.Thickness * svgUnits
			x, y, w := it.X*svgUnits, it.Y*svgUnits, it.Width*svgUnits
			if !it.Dashed {
				rect(x, y-t/2, x+w, y+t/2, fill)
				continue
			}
			dash := max(4*t, 2)
			for at := x; at < x+w; at += 2 * dash {
				rect(at, y-t/2, min(at+dash, x+w), y+t/2, fill)
			}
		case Rect:
			x, y := it.X*svgUnits, it.Y*svgUnits
			rect(x, y, x+it.Width*svgUnits, y+it.Height*svgUnits, fill)
		case Path:
			svgPath(&b, it, fill, fg)
		}
	}
	b.WriteString("</svg>")
	return b.String()
}

// num writes v with at most digits decimals, without trailing zeros.
func num(v float32, digits int) string {
	s := strconv.FormatFloat(float64(v), 'f', digits, 32)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

// glyphPath writes the path data of outline o, its origin at x, y, scale
// units to a unit of its font, whose y is up.
func glyphPath(b *strings.Builder, o *outline, x, y, scale float32) {
	pt := func(s opentype.SegmentPoint) string {
		return num(x+s.X*scale, 1) + " " + num(y-s.Y*scale, 1)
	}
	open := false
	for _, s := range o.segments {
		switch s.Op {
		case opentype.SegmentOpMoveTo:
			if open {
				b.WriteString("Z")
			}
			b.WriteString("M" + pt(s.Args[0]))
			open = true
		case opentype.SegmentOpLineTo:
			b.WriteString("L" + pt(s.Args[0]))
		case opentype.SegmentOpQuadTo:
			b.WriteString("Q" + pt(s.Args[0]) + " " + pt(s.Args[1]))
		case opentype.SegmentOpCubeTo:
			b.WriteString("C" + pt(s.Args[0]) + " " + pt(s.Args[1]) + " " + pt(s.Args[2]))
		}
	}
	if open {
		b.WriteString("Z")
	}
}

// svgPath writes a path item as drawPath draws it: each part filled on its
// own, or else stroked.
func svgPath(b *strings.Builder, it Item, fill, fg string) {
	pt := func(x, y float32) string {
		return num((it.X+x)*svgUnits, 1) + " " + num((it.Y+y)*svgUnits, 1)
	}
	var d strings.Builder
	flush := func() {
		if d.Len() == 0 {
			return
		}
		if it.Fill {
			fmt.Fprintf(b, `<path d="%s"%s/>`, d.String(), fill)
		} else {
			stroke := `stroke="` + html.EscapeString(fg) + `"`
			if fill != "" {
				stroke = strings.Replace(strings.TrimSpace(fill), "fill=", "stroke=", 1)
			}
			// 1.5 px of a 16 px text, as Draw strokes it.
			fmt.Fprintf(b, `<path d="%s" fill="none" %s stroke-width="%s"/>`, d.String(), stroke, num(1.5*svgUnits/16, 0))
		}
		d.Reset()
	}
	for _, c := range it.Commands {
		if c.Type == MoveTo || d.Len() == 0 {
			flush()
			if c.Type != MoveTo {
				d.WriteString("M" + pt(0, 0))
			}
		}
		switch c.Type {
		case MoveTo:
			d.WriteString("M" + pt(c.X, c.Y))
		case LineTo:
			d.WriteString("L" + pt(c.X, c.Y))
		case QuadTo:
			d.WriteString("Q" + pt(c.X1, c.Y1) + " " + pt(c.X, c.Y))
		case CubicTo:
			d.WriteString("C" + pt(c.X1, c.Y1) + " " + pt(c.X2, c.Y2) + " " + pt(c.X, c.Y))
		case Close:
			d.WriteString("Z")
		}
	}
	flush()
}
