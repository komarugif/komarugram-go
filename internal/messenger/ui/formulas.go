// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"strings"
	"unicode/utf8"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/formula"
	"komarugram/internal/messenger/styledtext"
	"komarugram/pkg/ratex"
)

// Formulas are laid out by RaTeX (internal/messenger/formula) off the
// frame: until one is, and when it cannot be, its source shows in the
// code's font, as Telegram Desktop shows a formula it cannot draw.

// Sizes of formulas: a display formula is set 1.21 times its text, as
// KaTeX sets one; one drawn past maxFormula px is shown as its source
// (`\rule{100000em}…` is valid).
const (
	displayFormulaScale = 1.21
	maxFormula          = 4096
)

// formula returns source laid out, in display style or in text style, once
// it is; until then, and when it cannot be, nil, and the frame after it is
// laid out is asked for.
func (p *chatPage) formula(source string, display bool) *ratex.List {
	key := formula.KeyOf(source, display)
	res, ok := formula.Lookup(key)
	if !ok {
		formula.Request(key, source, display, p.invalidate)
		return nil
	}
	return res.List
}

// formulaFits reports whether list drawn em px to an em is small enough to
// draw, wide at most width px when width is not 0.
func formulaFits(list *ratex.List, em float32, width int) bool {
	size, _ := list.Size(em)
	return size.X > 0 && size.Y > 0 && size.Y <= maxFormula && size.X <= maxFormula*4 && (width == 0 || size.X <= width)
}

// drawFormula draws list em px to an em, the top left of its box at the
// origin, in fg where it sets no colour of its own.
func drawFormula(gtx layout.Context, list *ratex.List, em float32, fg color.NRGBA) {
	list.Draw(gtx, em, fg, formulaGlyph)
}

// formulaGlyph draws a character no KaTeX font has, as Cyrillic in \text,
// with the client's text font.
func formulaGlyph(gtx layout.Context, r rune, size float32, origin f32.Point, col color.NRGBA) {
	th := wdk.GetMaterialTheme(gtx)
	gtx.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
	material := op.Record(gtx.Ops)
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	textColor := material.Stop()
	macro := op.Record(gtx.Ops)
	dims := widget.Label{MaxLines: 1}.Layout(gtx, th.TextShaper, font.Font{Typeface: th.Typescale[token.TypestyleBodyLarge].Font}, unit.Sp(size/gtx.Metric.PxPerSp), string(r), textColor)
	call := macro.Stop()
	defer op.Offset(image.Pt(int(origin.X+.5), int(origin.Y+.5)-dims.Baseline)).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
}

// leafText is the text of a leaf of r.
func (r *messageRow) leafText(b *messageTextBlock) string {
	var s strings.Builder
	for _, run := range r.runs[b.first:b.end] {
		s.WriteString(run.Text)
	}
	return s.String()
}

// formulaFragment makes the formula of leaf b, drawn at bounds in the row's
// text, its text there: one cluster of all of its source, which selects
// and copies as text does, its selection painted at local, where it is
// drawn.
func (p *chatPage) formulaFragment(gtx layout.Context, r *messageRow, b *messageTextBlock, bounds image.Rectangle, local image.Point) {
	runes := utf8.RuneCountInString(r.leafText(b))
	f := ratexFragment(b, bounds, runes)
	r.text.fragments = append(r.text.fragments, f)
	if p.activeText != r {
		return
	}
	selection := textInteraction{fragments: r.text.fragments[len(r.text.fragments)-1:], anchor: r.text.anchor, caret: r.text.caret}
	for _, rect := range selection.selectionRegions() {
		rect = rect.Sub(bounds.Min).Add(local)
		paint.FillShape(gtx.Ops, scheme(gtx).Primary.Color.SetOpacity(.28).AsNRGBA(), clip.Rect(rect).Op())
	}
}

// ratexFragment is the fragment of a formula leaf b drawn at bounds: its
// first run, and one cluster of its runes.
func ratexFragment(b *messageTextBlock, bounds image.Rectangle, runes int) styledtext.Fragment {
	return styledtext.Fragment{Index: b.first, Bounds: bounds, Clusters: []styledtext.Cluster{{Bounds: bounds, Start: b.runeStart, End: b.runeStart + runes}}}
}
