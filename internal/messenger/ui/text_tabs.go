// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"

	"komarugram/internal/messenger/styledtext"

	"gioui.org/layout"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"
)

// tabSpaces is how many spaces a tab is as wide as.
const tabSpaces = 4

// tabBox is the box a tab stands in, in text of style st: as wide as
// tabSpaces spaces of its font, on the text's baseline. The fonts have no
// glyph for a tab, and drew a box in its place; the tab stays in the
// span's content, so that it selects and copies as it is.
func tabBox(gtx layout.Context, shaper *text.Shaper, st styledtext.SpanStyle) styledtext.Box {
	shaper.LayoutString(text.Parameters{Font: st.Font, PxPerEm: fixed.I(gtx.Sp(st.Size)), MaxWidth: 1 << 20}, strings.Repeat(" ", tabSpaces))
	var width fixed.Int26_6
	var ascent, descent fixed.Int26_6
	for g, ok := shaper.NextGlyph(); ok; g, ok = shaper.NextGlyph() {
		width += g.Advance
		ascent, descent = max(ascent, g.Ascent), max(descent, g.Descent)
	}
	return styledtext.Box{Size: image.Pt(width.Ceil(), (ascent + descent).Ceil()), Ascent: ascent.Ceil()}
}

// splitTabs cuts the spans of a flow that hold tabs, each tab a span of its
// own in a box (tabBox); runs maps each span to its run, and is cut along.
func splitTabs(gtx layout.Context, shaper *text.Shaper, spans []styledtext.SpanStyle, runs []int) ([]styledtext.SpanStyle, []int) {
	has := false
	for _, s := range spans {
		has = has || s.Box == nil && strings.Contains(s.Content, "\t")
	}
	if !has {
		return spans, runs
	}
	outSpans := make([]styledtext.SpanStyle, 0, len(spans)+4)
	outRuns := make([]int, 0, len(runs)+4)
	boxes := map[styledtext.SpanStyle]*styledtext.Box{}
	for i, s := range spans {
		if s.Box != nil || !strings.Contains(s.Content, "\t") {
			outSpans, outRuns = append(outSpans, s), append(outRuns, runs[i])
			continue
		}
		key := styledtext.SpanStyle{Font: s.Font, Size: s.Size}
		box := boxes[key]
		if box == nil {
			b := tabBox(gtx, shaper, s)
			box = &b
			boxes[key] = box
		}
		rest := s.Content
		for rest != "" {
			piece := s
			if j := strings.IndexByte(rest, '\t'); j > 0 {
				piece.Content, rest = rest[:j], rest[j:]
			} else if j == 0 {
				piece.Content, rest = "\t", rest[1:]
				piece.Box = box
			} else {
				piece.Content, rest = rest, ""
			}
			outSpans, outRuns = append(outSpans, piece), append(outRuns, runs[i])
		}
	}
	return outSpans, outRuns
}
