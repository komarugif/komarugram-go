// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"strings"
	"unicode/utf8"

	"gioui.org/unit"

	"komarugram/internal/messenger/formula"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// The history knows how high a row is once it lays it out; until then it
// guesses, and the scrollbar, jumps and the place the history keeps move
// when a guess was wrong. A rich message's summary says little of its
// article's height: its photos, tables and code. articleHeight guesses it
// from the article's blocks as the layout sizes them (article_layout.go,
// article_blocks.go): its media exactly, by the same functions, and its
// text by lines of an average width of letters.

// Sizes of the article's text, in sp at a size of 1, measured from the
// layout: a line's height, and the average advance of a letter in it,
// spaces included; and a line of code's.
const (
	articleLineHeight = 22.4
	articleLetter     = 6.6
	articleCodeLine   = 20
	articleCodeLetter = 8.4
)

// articleEstimate guesses how high the article of rich message m is drawn
// in a bubble of a history width px wide at metric, with its "Show more".
func articleEstimate(m model.Message, l localization.Catalog, metric unit.Metric, width int) int {
	if m.Rich == nil || len(m.Rich.Blocks) == 0 {
		return 0
	}
	doc := prepareArticle(*m.Rich, l, m.Date)
	// The bubble is at most 660 dp wide, 16 and 20 dp in from the row's
	// edges, and holds what it shows 12 dp in from its own.
	content := max(1, min(width-metric.Dp(36), metric.Dp(660))-metric.Dp(24))
	h := doc.height(metric, doc.blocks, content)
	if doc.part {
		h += metric.Dp(42)
	}
	return h
}

// height guesses how high blocks are drawn one under another, width wide.
func (d *articleDoc) height(m unit.Metric, blocks []*articleBlock, width int) int {
	y := 0
	for i, b := range blocks {
		if i > 0 && b.kind != articleAnchor {
			y += m.Dp(unit.Dp(b.skip))
		}
		y += d.blockHeight(m, b, width)
	}
	return y
}

// blockHeight guesses how high b is drawn width wide.
func (d *articleDoc) blockHeight(m unit.Metric, b *articleBlock, width int) int {
	dp := func(v unit.Dp) int { return m.Dp(v) }
	switch b.kind {
	case articleFlow:
		return d.textHeight(m, b.leaf, width)
	case articleCode:
		return dp(49) + d.codeLines(m, b.leaf, width-dp(24))*m.Sp(articleCodeLine)
	case articleMath:
		// A formula laid out already is as high as it is drawn.
		if b.leaf >= 0 {
			if res, ok := formula.Lookup(formula.KeyOf(d.leafText(b.leaf), true)); ok && res.List != nil {
				em := float32(m.Sp(16)) * displayFormulaScale
				if formulaFits(res.List, em, 0) {
					size, _ := res.List.Size(em)
					return size.Y + dp(12)
				}
			}
		}
		return dp(12) + d.codeLines(m, b.leaf, width-dp(20))*m.Sp(articleCodeLine)
	case articleQuote:
		if b.leaf >= 0 {
			return dp(12) + d.textHeight(m, b.leaf, width-dp(40)) + d.below(m, b.caption, width, 4)
		}
		return dp(12) + d.height(m, b.children, width-dp(20)) + d.below(m, b.caption, width, 4)
	case articlePullquote:
		return dp(16) + d.textHeight(m, b.leaf, width) + d.height(m, b.children, width) + d.below(m, b.caption, width, 4)
	case articleList:
		y := 0
		for i, it := range b.items {
			if i > 0 {
				y += dp(3)
			}
			h := 0
			if it.leaf >= 0 {
				h = d.textHeight(m, it.leaf, width-dp(32))
			} else {
				h = d.height(m, it.blocks, width-dp(32))
			}
			y += max(h, m.Sp(articleLineHeight))
		}
		return y
	case articleDivider:
		return dp(9)
	case articleTable:
		y := d.textHeight(m, b.title, width)
		if y > 0 {
			y += dp(6)
		}
		cell := max(1, width/max(b.columns, 1)-dp(16))
		// Columns narrower than tdesktop's least width make the table
		// wider than the article, with a scrollbar under it.
		if b.columns*dp(118) > width {
			cell = dp(118) - dp(16)
			y += dp(13)
		}
		for _, row := range b.rows {
			h := m.Sp(articleLineHeight)
			for _, c := range row.cells {
				h = max(h, d.textHeight(m, c.leaf, cell*c.colspan))
			}
			y += h + dp(12)
		}
		return y
	case articleDetails:
		h := max(d.textHeight(m, b.title, width-dp(52)), dp(24)) + dp(16)
		if b.open {
			h += d.height(m, b.children, width-dp(20)) + dp(8)
		}
		return h
	case articleMedia:
		return mediaBlockHeight(m, b, width) + d.below(m, b.caption, width, 6)
	case articleButtons:
		return dp(34)
	case articleEmbedPost:
		return dp(36) + d.height(m, b.children, width-dp(20)) + d.below(m, b.caption, width, 4)
	case articleCard:
		n := max(len(b.card.related), 1)
		y := d.textHeight(m, b.title, width)
		if y > 0 {
			y += dp(6)
		}
		return y + n*dp(56) + (n-1)*dp(6) + d.below(m, b.caption, width, 6)
	}
	return 0
}

// below is the height of a leaf under a block, skip apart; 0 for none.
func (d *articleDoc) below(m unit.Metric, leaf, width int, skip unit.Dp) int {
	if h := d.textHeight(m, leaf, width); h > 0 {
		return m.Dp(skip) + h
	}
	return 0
}

// textHeight guesses how high the text of leaf is drawn width wide: its
// lines, each as high as its size asks for.
func (d *articleDoc) textHeight(m unit.Metric, leaf, width int) int {
	if leaf < 0 {
		return 0
	}
	scale := d.leaves[leaf].style.scale
	if scale == 0 {
		scale = 1
	}
	letter := articleLetter * scale * m.PxPerSp
	return d.lines(leaf, width, letter) * int(articleLineHeight*scale*m.PxPerSp+.5)
}

// codeLines guesses how many lines of code leaf takes width wide.
func (d *articleDoc) codeLines(m unit.Metric, leaf, width int) int {
	if leaf < 0 {
		return 0
	}
	return d.lines(leaf, width, articleCodeLetter*m.PxPerSp)
}

// leafText is the text of leaf.
func (d *articleDoc) leafText(leaf int) string {
	var text strings.Builder
	b := d.leaves[leaf]
	for _, r := range d.runs[b.first:b.end] {
		text.WriteString(r.Text)
	}
	return text.String()
}

// lines guesses how many lines the text of leaf takes width wide, with
// letters letter px wide: each of its own lines wraps at that width.
func (d *articleDoc) lines(leaf, width int, letter float32) int {
	var text strings.Builder
	b := d.leaves[leaf]
	for _, r := range d.runs[b.first:b.end] {
		text.WriteString(r.Text)
	}
	perLine := max(1, int(float32(width)/letter))
	n := 0
	for line := range strings.SplitSeq(text.String(), "\n") {
		n += max(1, (utf8.RuneCountInString(line)+perLine-1)/perLine)
	}
	return n
}

// mediaBlockHeight is how high the media of block b are drawn width wide,
// without its caption.
func mediaBlockHeight(m unit.Metric, b *articleBlock, width int) int {
	switch {
	case len(b.media) == 1:
		return itemHeight(m, b.media[0], width)
	case b.slideshow:
		return slideshowHeight(m, b.media, width)
	case len(b.media) > 1:
		return collageHeight(m, b.media, width)
	}
	return 0
}

// itemHeight is how high one item of media is drawn width wide: a photo
// or a video as its shape asks for, bounded; anything else as a card.
func itemHeight(m unit.Metric, media model.RichMedia, width int) int {
	if !visual(media) {
		return m.Dp(56)
	}
	h, _ := mediaHeight(m, media, width)
	return h
}

// collageHeight is how high a collage of items is drawn width wide, as
// collage lays them out.
func collageHeight(m unit.Metric, items []model.RichMedia, width int) int {
	gap := m.Dp(2)
	y := 0
	for i := 0; i < len(items); i += 2 {
		if i > 0 {
			y += gap
		}
		row := items[i:min(i+2, len(items))]
		if len(row) == 1 || !visual(row[0]) || !visual(row[1]) {
			for j, item := range row {
				if j > 0 {
					y += gap
				}
				y += itemHeight(m, item, width)
			}
			continue
		}
		h, _ := pairRow(m, [2]model.RichMedia{row[0], row[1]}, width)
		y += h
	}
	return y
}
