// Package styledtext provides rendering of text containing multiple fonts and styles.
package styledtext

import (
	"image"
	"image/color"
	"unicode/utf8"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"golang.org/x/image/math/fixed"
)

// SpanStyle describes the appearance of a span of styled text.
type SpanStyle struct {
	Font    font.Font
	Size    unit.Sp
	Color   color.NRGBA
	Content string
	// Shift moves the span down from the top of its line, as a
	// subscript's: spans of a line are set at its top.
	Shift unit.Sp
	// Box, when set, makes the span an object of that size in its line, as
	// an inline formula, in place of its text: Content is what it stands
	// for, to select and copy, and Decorate draws it. A line with a box is
	// set on one baseline: its text, as high as it is, under the boxes'
	// tops.
	Box *Box

	idx   int
	start int
	// shaped is Content followed by a word joiner, and runes counts Content.
	// A line break keeps a suffix of both, so a wrapped span is sliced
	// instead of being copied and recounted for every line.
	shaped string
	runes  int
}

// Box is the size of an object in a line of text, and where its baseline
// is under its top.
type Box struct {
	Size   image.Point
	Ascent int
}

// spanShape describes the text shaping of a single span.
type spanShape struct {
	offset   image.Point
	call     op.CallOp
	size     image.Point
	ascent   int
	clusters []Cluster
	// shift is the span's Shift in pixels.
	shift int
	box   bool
}

// Layout renders the span using the provided text shaping.
func (ss SpanStyle) Layout(gtx layout.Context, shape spanShape) layout.Dimensions {
	paint.ColorOp{Color: ss.Color}.Add(gtx.Ops)
	defer op.Offset(shape.offset).Push(gtx.Ops).Pop()
	shape.call.Add(gtx.Ops)
	return layout.Dimensions{Size: shape.size}
}

// WrapPolicy defines line wrapping policies for styledtext. Due to complexities
// of the styledtext implementation, there are fewer options available than in
// [gioui.org/text.WrapPolicy].
type WrapPolicy uint8

const (
	// WrapWords implements behavior like [gioui.org/text/.WrapWords]. This is the default,
	// as it prevents words from being split across lines.
	WrapWords WrapPolicy = iota
	// WrapWords implements behavior like [gioui.org/text/.WrapGraphemes]. This often gives
	// unpleasant results, as it will choose to split words across lines whenever it can. Some
	// use-cases may still want this, however.
	WrapGraphemes
)

func (s WrapPolicy) textPolicy() text.WrapPolicy {
	switch s {
	case WrapWords:
		return text.WrapWords
	default:
		return text.WrapGraphemes
	}
}

// Cluster is a shaped, indivisible text cluster. Rune offsets refer to the
// concatenated original spans; Bounds are relative to the entire text block.
type Cluster struct {
	Bounds     image.Rectangle
	Start, End int
	RTL        bool
}

// Fragment is the part of a span on one line, in text-block coordinates.
type Fragment struct {
	Index    int
	Bounds   image.Rectangle
	Clusters []Cluster
}

// TextStyle presents rich text.
type TextStyle struct {
	// Decorate, when set, paints a fragment instead of the default painter.
	// The context and draw callback use fragment-local coordinates. Call draw
	// inside a clip to reveal text without changing its shaping or wrapping.
	Decorate func(layout.Context, Fragment, func())
	// MaxLines limits the visible lines; zero means all. Hidden lines are
	// neither drawn nor exposed to hit testing.
	MaxLines int
	// Clusters, when not nil, is reused as the storage of Fragment.Clusters.
	// A text laid out every frame then allocates nothing per glyph; fragments
	// from an earlier Layout with the same buffer are overwritten.
	Clusters   *[]Cluster
	Styles     []SpanStyle
	Alignment  text.Alignment
	WrapPolicy WrapPolicy
	*text.Shaper
}

// Text constructs a TextStyle.
func Text(shaper *text.Shaper, styles ...SpanStyle) TextStyle {
	return TextStyle{
		Styles: styles,
		Shaper: shaper,
	}
}

type spanResults struct {
	call             op.CallOp
	width            int
	height           int
	ascent           int
	runes            int
	multiLine        bool
	endedWithNewline bool
	clusters         []Cluster
}

// iterateSpan shapes shaped, the start of span that has runeCount runes,
// or all of it: one line of it when truncate is set.
func (t TextStyle) iterateSpan(gtx layout.Context, maxWidth int, span SpanStyle, shaped string, runeCount int, truncate bool, clusters *[]Cluster) (op.CallOp, textIterator) {
	var glyphs [32]text.Glyph
	maxLines := 0
	if truncate {
		maxLines = 1
	}
	ppem := gtx.Sp(span.Size)
	// shape the text of the current span
	macro := op.Record(gtx.Ops)
	paint.ColorOp{Color: span.Color}.Add(gtx.Ops)
	t.Shaper.LayoutString(text.Parameters{
		Font:       span.Font,
		PxPerEm:    fixed.I(ppem),
		MaxLines:   maxLines,
		MaxWidth:   maxWidth,
		Truncator:  "\u200b", // Unicode zero-width space.
		Locale:     gtx.Locale,
		WrapPolicy: t.WrapPolicy.textPolicy(),
	}, shaped)
	ti := textIterator{
		color:    span.Color,
		hidden:   span.Color.A == 0,
		viewport: image.Rectangle{Max: gtx.Constraints.Max},
		maxLines: 1,
	}

	first := len(*clusters)
	line := glyphs[:0]
	var cluster image.Rectangle
	start := 0
	for g, ok := t.Shaper.NextGlyph(); ok; g, ok = t.Shaper.NextGlyph() {
		if g.Flags&text.FlagTruncator == 0 {
			box := image.Rect(g.X.Floor(), 0, (g.X + g.Advance).Ceil(), (g.Ascent + g.Descent).Ceil())
			cluster = cluster.Union(box)
			if g.Flags&text.FlagClusterBreak != 0 {
				end := min(start+int(g.Runes), runeCount)
				if end > start {
					*clusters = append(*clusters, Cluster{Bounds: cluster, Start: span.start + start, End: span.start + end, RTL: g.Flags&text.FlagTowardOrigin != 0})
				}
				start = end
				cluster = image.Rectangle{}
			}
		}
		line, ok = ti.paintGlyph(gtx, t.Shaper, g, line)
		if !ok {
			break
		}
	}
	ti.runes = min(ti.runes, runeCount)
	// Capped, so that later lines appending to the shared storage cannot
	// write into this line's clusters.
	ti.clusters = (*clusters)[first:len(*clusters):len(*clusters)]
	return macro.Stop(), ti
}

// lineRunesPerEm bounds how many runes of a line one em holds: no glyph
// but a zero-width one is narrower than a quarter of an em.
const lineRunesPerEm = 4

// shapeWholeSpans turns linePrefix off, for tests that compare with it.
var shapeWholeSpans = false

// linePrefix is the start of span that holds its first line maxWidth wide,
// ending before a space so that no word is cut, and its rune count: the
// whole span when it is short.
func linePrefix(span SpanStyle, maxWidth, ppem int) (string, int) {
	limit := lineRunesPerEm*maxWidth/max(ppem, 1) + 16
	if span.runes <= limit || shapeWholeSpans {
		return span.shaped, span.runes
	}
	n := 0
	for i, r := range span.Content {
		if n >= limit && (r == ' ' || r == '\n') {
			return span.Content[:i], n
		}
		n++
	}
	return span.shaped, span.runes
}

func (t TextStyle) layoutSpan(gtx layout.Context, maxWidth int, span SpanStyle, clusters *[]Cluster) spanResults {
	if b := span.Box; b != nil {
		// One cluster of all of Content, as wide and high as the box.
		*clusters = append(*clusters, Cluster{Bounds: image.Rectangle{Max: b.Size}, Start: span.start, End: span.start + span.runes})
		return spanResults{width: b.Size.X, height: b.Size.Y, ascent: b.Ascent, runes: span.runes, clusters: (*clusters)[len(*clusters)-1:]}
	}
	// One line needs only the start of the span: shaping all the rest of
	// a long span for each of its lines made wrapping quadratic.
	mark := len(*clusters)
	prefix, runes := linePrefix(span, maxWidth, gtx.Sp(span.Size))
	call, ti := t.iterateSpan(gtx, maxWidth, span, prefix, runes, true, clusters)
	if runes < span.runes && ti.runes >= runes {
		// All of the start fit on the line, which may go on past it.
		*clusters = (*clusters)[:mark]
		call, ti = t.iterateSpan(gtx, maxWidth, span, span.shaped, span.runes, true, clusters)
	}
	runesDisplayed := ti.runes
	multiLine := runesDisplayed < span.runes
	endedWithNewline := ti.hasNewline
	if multiLine {
		var i int
		for n := 0; n < runesDisplayed; n++ {
			_, sz := utf8.DecodeRuneInString(span.Content[i:])
			i += sz
		}
		firstTruncatedRune, _ := utf8.DecodeRuneInString(span.Content[i:])
		if firstTruncatedRune == '\n' {
			endedWithNewline = true
			runesDisplayed++
		} else if runesDisplayed == 0 {
			// Even grapheme wrapping needs an untruncated fallback when the
			// viewport is narrower than one glyph. Otherwise a truncator-only
			// line consumes nothing and Layout loops forever.
			call, ti = t.iterateSpan(gtx, maxWidth, span, span.shaped, span.runes, false, clusters)
			runesDisplayed = ti.runes
			multiLine = runesDisplayed < span.runes
			endedWithNewline = ti.hasNewline
		}
	}
	return spanResults{
		call:             call,
		width:            ti.bounds.Dx(),
		height:           ti.bounds.Dy(),
		ascent:           ti.baseline,
		runes:            runesDisplayed,
		multiLine:        multiLine,
		endedWithNewline: endedWithNewline,
		clusters:         ti.clusters,
	}
}

// Layout renders the TextStyle.
//
// The spanFn function, if not nil, gets called for each span after it has been
// drawn, with the offset set to the span's top left corner. This can be used to
// set up input handling, for example.
//
// The context's maximum constraint is set to the span's dimensions, while the
// dims argument additionally provides the text's baseline. The idx argument is
// the span's index in TextStyle.Styles. The function may get called multiple
// times with the same index if a span has to be broken across multiple lines.
func (t TextStyle) Layout(gtx layout.Context, spanFn func(gtx layout.Context, idx int, dims layout.Dimensions)) layout.Dimensions {
	spans := make([]SpanStyle, len(t.Styles))
	copy(spans, t.Styles)
	start := 0
	for i := range spans {
		spans[i].idx = i
		spans[i].start = start
		spans[i].shaped = spans[i].Content + "\u2060"
		spans[i].runes = utf8.RuneCountInString(spans[i].Content)
		start += spans[i].runes
	}
	// A cluster covers at least one rune, so one text needs at most start
	// clusters, apart from lines shaped again after a forced break.
	var clusters []Cluster
	if t.Clusters != nil {
		clusters = (*t.Clusters)[:0]
		defer func() { *t.Clusters = clusters }()
	}
	if cap(clusters) < start {
		clusters = make([]Cluster, 0, start)
	}

	var (
		lineDims       image.Point
		lineAscent     int
		overallSize    image.Point
		lineShapes     []spanShape
		lineStartIndex int
		lines          int
	)

	for i := 0; i < len(spans); i++ {
		// grab the next span
		span := spans[i]

		// constrain the width of the line to the remaining space
		maxWidth := gtx.Constraints.Max.X - lineDims.X

		res := t.layoutSpan(gtx, maxWidth, span, &clusters)

		// forceToNextLine handles the case in which the first segment of the new span does not fit
		// AND there is already content on the current line. If there is no content on the line,
		// we should display the content that doesn't fit anyway, as it won't fit on the next
		// line either.
		forceToNextLine := lineDims.X > 0 && res.width > maxWidth

		if !forceToNextLine {
			// store the text shaping results for the line
			shift := gtx.Sp(span.Shift)
			lineShapes = append(lineShapes, spanShape{
				offset:   image.Point{X: lineDims.X},
				size:     image.Point{X: res.width, Y: res.height},
				call:     res.call,
				ascent:   res.ascent,
				clusters: res.clusters,
				shift:    shift,
				box:      span.Box != nil,
			})
			// update the dimensions of the current line
			lineDims.X += res.width
			if lineDims.Y < res.height+shift {
				lineDims.Y = res.height + shift
			}
			if lineAscent < res.ascent {
				lineAscent = res.ascent
			}

			// update the width of the overall text
			if overallSize.X < lineDims.X {
				overallSize.X = lineDims.X
			}

		}

		// if we are breaking the current span across lines or we are on the
		// last span, lay out all of the spans for the line.
		if res.multiLine || res.endedWithNewline || i == len(spans)-1 || forceToNextLine {
			pad := 0
			if lineDims.X < gtx.Constraints.Max.X {
				switch t.Alignment {
				case text.Middle:
					pad = (gtx.Constraints.Max.X - lineDims.X) / 2
				case text.End:
					pad = gtx.Constraints.Max.X - lineDims.X
				}
			}
			// A line with a box is set on one baseline; others keep their
			// spans at the line's top.
			textAscent, boxAscent, boxed := 0, 0, false
			for _, shape := range lineShapes {
				if shape.box {
					boxed, boxAscent = true, max(boxAscent, shape.ascent)
				} else {
					textAscent = max(textAscent, shape.ascent)
				}
			}
			lineAscent = max(textAscent, boxAscent)
			if boxed {
				lineDims.Y = 0
				for _, shape := range lineShapes {
					top := lineAscent - textAscent + shape.shift
					if shape.box {
						top = lineAscent - shape.ascent
					}
					lineDims.Y = max(lineDims.Y, top+shape.size.Y)
				}
			}
			lineMacro := op.Record(gtx.Ops)
			for i, shape := range lineShapes {
				// lay out this span
				span = spans[i+lineStartIndex]
				shape.offset.Y = overallSize.Y + shape.shift
				if boxed {
					shape.offset.Y += lineAscent - textAscent
					if shape.box {
						shape.offset.Y = overallSize.Y + lineAscent - shape.ascent
					}
				}
				if t.Decorate == nil {
					span.Layout(gtx, shape)
				} else {
					bounds := image.Rectangle{Min: shape.offset.Add(image.Pt(pad, 0)), Max: shape.offset.Add(image.Pt(pad, 0)).Add(shape.size)}
					// Each shaped line owns its clusters, so they are moved in place.
					clusters := shape.clusters
					for j := range clusters {
						clusters[j].Bounds = clusters[j].Bounds.Add(bounds.Min)
					}
					stack := op.Offset(shape.offset).Push(gtx.Ops)
					local := shape
					local.offset = image.Point{}
					t.Decorate(gtx, Fragment{Index: span.idx, Bounds: bounds, Clusters: clusters}, func() { span.Layout(gtx, local) })
					stack.Pop()
				}

				if spanFn == nil {
					continue
				}
				offStack := op.Offset(shape.offset).Push(gtx.Ops)
				fnGtx := gtx
				fnGtx.Constraints.Min = image.Point{}
				fnGtx.Constraints.Max = shape.size
				spanFn(fnGtx, span.idx, layout.Dimensions{Size: shape.size, Baseline: shape.ascent})
				offStack.Pop()
			}
			lineCall := lineMacro.Stop()

			stack := op.Offset(image.Pt(pad, 0)).Push(gtx.Ops)
			lineCall.Add(gtx.Ops)
			stack.Pop()

			// reset line shaping data and update overall vertical dimensions
			lineShapes = lineShapes[:0]
			overallSize.Y += lineDims.Y
			if lineDims.Y > 0 {
				lines++
				if t.MaxLines > 0 && lines >= t.MaxLines {
					break
				}
			}
			lineDims = image.Point{}
			lineAscent = 0
		}

		// if the current span breaks across lines
		if res.multiLine && !forceToNextLine {
			// The line holding this span has been laid out, so the rest of
			// the span replaces it and starts the next line.
			lineStartIndex = i

			byteLen := 0
			for i := 0; i < res.runes; i++ {
				_, n := utf8.DecodeRuneInString(span.Content[byteLen:])
				byteLen += n
			}
			span.Content = span.Content[byteLen:]
			span.shaped = span.shaped[byteLen:]
			span.start += res.runes
			span.runes -= res.runes
			spans[i] = span
			i--
		} else if forceToNextLine {
			// mark where the next line to be laid out starts
			lineStartIndex = i
			i--
		} else if res.endedWithNewline {
			// mark where the next line to be laid out starts
			lineStartIndex = i + 1
		}
	}

	return layout.Dimensions{Size: gtx.Constraints.Constrain(overallSize)}
}
