// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image/color"
	"sort"
	"strings"

	"gio-mw/token"
	"gioui.org/layout"

	"komarugram/internal/messenger/codehighlight"
)

// codePalettes are the colors of code's classes (codehighlight.Class), in
// the light theme and the dark one. Telegram Desktop takes them from its
// chart palette; these follow Telegram for Android's groups (keyword,
// operator, constant, string, comment), toned for the code block's plate.
// Plain is the color of code with no class, and of inline code, as
// Telegram Desktop's monoFg (#4e7391 in its light theme).
var codePalettes = [2][codehighlight.Inserted + 1]color.NRGBA{
	{
		codehighlight.Plain:       {R: 0x4e, G: 0x73, B: 0x91, A: 0xff},
		codehighlight.Comment:     {R: 0x7d, G: 0x83, B: 0x8c, A: 0xff},
		codehighlight.Punctuation: {R: 0x5b, G: 0x64, B: 0x70, A: 0xff},
		codehighlight.Constant:    {R: 0xc8, G: 0x36, B: 0x36, A: 0xff},
		codehighlight.String:      {R: 0xb0, G: 0x5a, B: 0x0c, A: 0xff},
		codehighlight.Operator:    {R: 0x0a, G: 0x7d, B: 0xbb, A: 0xff},
		codehighlight.Keyword:     {R: 0x2f, G: 0x5c, B: 0xcc, A: 0xff},
		codehighlight.ClassName:   {R: 0x7c, G: 0x4a, B: 0xd6, A: 0xff},
		codehighlight.Inserted:    {R: 0x1f, G: 0x80, B: 0x3a, A: 0xff},
	},
	{
		codehighlight.Plain:       {R: 0x8f, G: 0xb8, B: 0xd8, A: 0xff},
		codehighlight.Comment:     {R: 0x8b, G: 0x94, B: 0x9e, A: 0xff},
		codehighlight.Punctuation: {R: 0xb1, G: 0xba, B: 0xc4, A: 0xff},
		codehighlight.Constant:    {R: 0xff, G: 0x7b, B: 0x72, A: 0xff},
		codehighlight.String:      {R: 0xff, G: 0xa6, B: 0x57, A: 0xff},
		codehighlight.Operator:    {R: 0x56, G: 0xc1, B: 0xff, A: 0xff},
		codehighlight.Keyword:     {R: 0x79, G: 0xa8, B: 0xff, A: 0xff},
		codehighlight.ClassName:   {R: 0xd2, G: 0xa8, B: 0xff, A: 0xff},
		codehighlight.Inserted:    {R: 0x7e, G: 0xe7, B: 0x87, A: 0xff},
	},
}

// codeColor is the color of class c in gtx's theme.
func codeColor(gtx layout.Context, c codehighlight.Class) color.NRGBA {
	dark := 0
	if token.IsDarkColorSet(scheme(gtx).Surface) {
		dark = 1
	}
	return codePalettes[dark][c]
}

// codeHighlight is what a code block knows of its colors.
type codeHighlight struct {
	requested, ready bool
	key              codehighlight.Key
	spans            []codehighlight.Span
}

// codeSpans are the colors of code block b, as Telegram Desktop colors a
// pre entity with a language: asked for in the background the first time,
// and drawn once they come.
func (p *chatPage) codeSpans(r *messageRow, b *messageTextBlock) []codehighlight.Span {
	h := &b.code
	if h.ready {
		return h.spans
	}
	language := r.runs[b.first].Language
	if strings.TrimSpace(language) == "" {
		h.ready = true
		return nil
	}
	if !h.requested {
		var text strings.Builder
		for _, run := range r.runs[b.first:b.end] {
			text.WriteString(run.Text)
		}
		h.key = codehighlight.KeyOf(language, text.String())
		h.requested = true
		invalidate := p.invalidate
		codehighlight.Request(h.key, language, text.String(), func() {
			if invalidate != nil {
				invalidate()
			}
		})
	}
	h.spans, h.ready = codehighlight.Lookup(h.key)
	return h.spans
}

// codePiece is bytes start to end of a run's text, in a class.
type codePiece struct {
	start, end int
	class      codehighlight.Class
}

// codePieces cuts a run's text, which begins at byte base of its block, at
// the bounds of the block's spans.
func codePieces(text string, base int, spans []codehighlight.Span) []codePiece {
	end := base + len(text)
	i := sort.Search(len(spans), func(i int) bool { return spans[i].End > base })
	var out []codePiece
	pos := base
	for ; i < len(spans) && spans[i].Start < end; i++ {
		s := spans[i]
		if start := max(s.Start, base); start > pos {
			out = append(out, codePiece{pos - base, start - base, codehighlight.Plain})
			pos = start
		}
		stop := min(s.End, end)
		out = append(out, codePiece{pos - base, stop - base, s.Class})
		pos = stop
	}
	if pos < end {
		out = append(out, codePiece{pos - base, end - base, codehighlight.Plain})
	}
	return out
}
