// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"komarugram/internal/messenger/model"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// A skeleton whose row's profile is known draws in its bubble what the row
// will show: a bar for each line of its text, as long as the line, and a
// block for its media, the quote it replies with, its reactions and the
// widgets it has, where the profile puts them at the history's width
// (rowProfile.predictShape). It is worked out when the skeleton comes into
// view, and only while animations are on: with them off, the skeletons are
// plain, which costs less.

// shapeBar is a part of what a skeleton's bubble shows, in the bubble.
type shapeBar struct {
	rect image.Rectangle
	kind barKind
}

// barKind is what a shapeBar stands for.
type barKind uint8

const (
	// barLine is a line of text.
	barLine barKind = iota
	// barBlock is media, a quote, reactions or a widget.
	barBlock
)

// appendShape appends to out what a part of kind laid out at r shows: a
// block, or, for text of profile t wrapped at width at, its lines.
func appendShape(out []shapeBar, kind bubblePartKind, r image.Rectangle, t *textProfile, at int) []shapeBar {
	if kind != partText {
		return append(out, shapeBar{rect: r, kind: barBlock})
	}
	if t == nil {
		return out
	}
	y := r.Min.Y
	for i := range t.blocks {
		b := &t.blocks[i]
		if i > 0 {
			y += t.gap
		}
		top := y + b.chrome/2
		_, h, _, _, _ := b.wrapEach(at, func(w, h int) {
			// A bar of the middle of the line, as the letters are.
			pad := h / 5
			out = append(out, shapeBar{rect: image.Rect(r.Min.X+b.left, top+pad, r.Min.X+b.left+w, top+h-pad), kind: barLine})
			top += h
		})
		y += b.chrome + h
	}
	return out
}

// skeletonShape is what a skeleton's profile tells at a width: its
// bubble, the room of the avatar beside it, and, when barred, what the
// bubble shows.
type skeletonShape struct {
	width  int
	bubble image.Point
	slot   int
	bars   []shapeBar
	barred bool
}

// shapeCacheSize is how many skeletons' shapes the history keeps: about
// those in view, and some it scrolled past.
const shapeCacheSize = 512

// shapeOf is what skeleton s's profile tells at the history's width, its
// bars too where asked, worked out once and kept; false without a profile.
// Profiles are decoded only for the skeletons drawn: thousands come with
// the reserve and the rows let go of.
func (p *chatPage) shapeOf(s skeletonRow, bars bool) (skeletonShape, bool) {
	if s.profile == nil {
		return skeletonShape{}, false
	}
	if c, ok := p.shapeCache[s.id]; ok && c.width == p.env.WidthPx && (c.barred || !bars) {
		return c, true
	}
	prof, ok, err := decodeRowProfile(s.profile)
	if !ok || err != nil {
		return skeletonShape{}, false
	}
	metric := unit.Metric{PxPerDp: float32(p.env.ScaleMilli) / 1000, PxPerSp: float32(p.env.TextScaleMilli) / 1000}
	c := skeletonShape{width: p.env.WidthPx, slot: prof.bubble.slot, barred: bars}
	var out *[]shapeBar
	if bars {
		out = &c.bars
	}
	h, w, _ := prof.predictShape(metric, p.env.WidthPx, out)
	c.bubble = image.Pt(w, max(0, h-prof.bubble.rest))
	if p.shapeCache == nil || len(p.shapeCache) >= shapeCacheSize {
		p.shapeCache = make(map[model.MessageID]skeletonShape, shapeCacheSize)
	}
	p.shapeCache[s.id] = c
	return c, true
}

// skeletonBars is what skeleton s's bubble shows at the history's width;
// nil without a profile.
func (p *chatPage) skeletonBars(s skeletonRow) []shapeBar {
	c, _ := p.shapeOf(s, true)
	return c.bars
}

// resolveShape is skeleton s with its bubble, where only its profile tells
// it, worked out as it comes into view; with its bars too while animations
// are on, which are drawn then.
func (p *chatPage) resolveShape(gtx layout.Context, s skeletonRow) skeletonRow {
	if s.bubble.X > 0 {
		return s
	}
	if c, ok := p.shapeOf(s, wdk.AnimationsEnabled(gtx)); ok {
		s.bubble, s.slot, s.shape = c.bubble, c.slot, shapeProfile
	}
	return s
}

// drawBars draws in bubble, the skeleton of s, what it shows, in a tone of
// col, while animations are on.
func (p *chatPage) drawBars(gtx layout.Context, s skeletonRow, bubble image.Rectangle, col token.MatColor) {
	if !wdk.AnimationsEnabled(gtx) {
		return
	}
	bars := p.skeletonBars(s)
	if len(bars) == 0 {
		return
	}
	defer clip.Rect(bubble).Push(gtx.Ops).Pop()
	line := col.SetOpacity(.1).AsNRGBA()
	block := col.SetOpacity(.07).AsNRGBA()
	radius := gtx.Dp(8)
	for _, b := range bars {
		r := b.rect.Add(bubble.Min)
		if r.Empty() {
			continue
		}
		switch b.kind {
		case barLine:
			paint.FillShape(gtx.Ops, line, clip.UniformRRect(r, r.Dy()/2).Op(gtx.Ops))
		default:
			paint.FillShape(gtx.Ops, block, clip.UniformRRect(r, min(radius, r.Dy()/2)).Op(gtx.Ops))
		}
	}
}
