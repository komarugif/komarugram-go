// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/styledtext"

	"gioui.org/font"
	"gioui.org/text"

	"gio-mw/token"
	"gio-mw/wdk"
	"gioui.org/io/clipboard"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// messageTextBlock is a flow of inline runs, optionally wrapped in a code or
// quote plate. All hit regions retain offsets into the original message.
type messageTextBlock struct {
	first, end, runeStart int
	trimStart, trimEnd    bool
	clusters              []styledtext.Cluster
	action                surface
	expanded              bool
	height                heightTransition
	maxLines              int
	// code is a code block's colors.
	code codeHighlight
	// style is how a flow of an article is set; a message's text is set
	// with the zero style.
	style flowStyle
}

// flowStyle is how a flow of an article is set, against a message's text:
// its size times scale (1 for 0), its weight, its color the supplementary
// one when dim, italic, and its alignment.
type flowStyle struct {
	scale  float32
	weight font.Weight
	dim    bool
	italic bool
	align  text.Alignment
}

func (r *messageRow) prepareTextBlocks() {
	if r.textBlocks != nil {
		return
	}
	runeStart := 0
	for i := 0; i < len(r.runs); {
		end, n := i+1, utf8.RuneCountInString(r.runs[i].Text)
		for end < len(r.runs) && r.runs[end].Block == r.runs[i].Block {
			n += utf8.RuneCountInString(r.runs[end].Text)
			end++
		}
		b := messageTextBlock{first: i, end: end, runeStart: runeStart}
		if r.runs[i].Block == 0 {
			// The block itself supplies a line break. Keep the source newline
			// for copying, but do not draw a second empty line beside it.
			b.trimStart = i > 0 && strings.HasPrefix(r.runs[i].Text, "\n")
			b.trimEnd = end < len(r.runs) && strings.HasSuffix(r.runs[end-1].Text, "\n")
		}
		r.textBlocks = append(r.textBlocks, b)
		runeStart += n
		i = end
	}
}

func (r *messageRow) copyTextBlock(b *messageTextBlock) string {
	if r.noCopy {
		return ""
	}
	var text strings.Builder
	for _, run := range r.runs[b.first:b.end] {
		if run.Spoiler && !r.revealed {
			text.WriteString("[•••]")
		} else {
			text.WriteString(run.Text)
		}
	}
	return text.String()
}

// textBlockAction does what b's button was pressed for: a code block's
// copies its text, a collapsed quote's opens or closes it.
func (p *chatPage) textBlockAction(gtx layout.Context, r *messageRow, b *messageTextBlock, l localization.Catalog) {
	if !b.action.Clicked(gtx) {
		return
	}
	run := r.runs[b.first]
	if run.Pre {
		if text := r.copyTextBlock(b); text != "" {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
			p.toast.Show(l.T("text.copied"))
		}
	} else if run.Quote && run.Collapsed {
		b.expanded = !b.expanded
		gtx.Execute(op.InvalidateCmd{})
	}
}

func (p *chatPage) richText(gtx layout.Context, r *messageRow, l localization.Catalog, animate bool) layout.Dimensions {
	end := p.trace.Begin("history.rich-text")
	defer end()
	r.prepareTextBlocks()
	for i := range r.textBlocks {
		p.textBlockAction(gtx, r, &r.textBlocks[i], l)
	}
	p.textEvents(gtx, r, animate)
	r.text.fragments = r.text.fragments[:0]
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	size := image.Point{}
	for i := range r.textBlocks {
		b := &r.textBlocks[i]
		if b.end-b.first == 1 && r.runs[b.first].Block == 0 && r.runs[b.first].Text == "\n" && (b.trimStart || b.trimEnd) {
			continue
		}
		if size.Y > 0 {
			size.Y += gtx.Dp(6)
		}
		origin := image.Pt(0, size.Y)
		stack := op.Offset(origin).Push(gtx.Ops)
		dims := p.textBlock(gtx, r, b, origin, l, animate)
		stack.Pop()
		size.X = max(size.X, dims.Size.X)
		size.Y += dims.Size.Y
	}
	call := macro.Stop()
	typing, lines, height := p.typingStep(gtx, r, size, animate)
	size.Y = height
	r.text.size = size
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	pointer.CursorText.Add(gtx.Ops)
	r.text.clicker.Add(gtx.Ops)
	r.text.dragger.Add(gtx.Ops)
	entityCursors(gtx, r)
	area.Pop()
	// Controls are registered after the text area, so their presses cannot
	// start a text selection or activate a link underneath a button.
	p.typed(gtx, r, typing, lines, call, size)
	return layout.Dimensions{Size: size}
}

func (p *chatPage) textBlock(gtx layout.Context, r *messageRow, b *messageTextBlock, origin image.Point, l localization.Catalog, animate bool) layout.Dimensions {
	run := r.runs[b.first]
	if run.Block == 0 {
		return p.textFlow(gtx, r, b, origin, animate)
	}
	if run.Quote {
		return p.quoteBlock(gtx, r, b, origin, l, animate)
	}
	pad := min(gtx.Dp(10), gtx.Constraints.Max.X/2)
	inner := gtx
	inner.Constraints.Max.X = max(1, gtx.Constraints.Max.X-2*pad)
	pos := image.Pt(pad, gtx.Dp(6))
	macro := op.Record(gtx.Ops)
	if run.Pre {
		stack := op.Offset(pos).Push(gtx.Ops)
		header := layout.Flex{Alignment: layout.Middle}.Layout(inner,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, codeLanguageLabel(run.Language), token.TypestyleLabelMedium, scheme(gtx).SurfaceVariant.OnColor, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if r.noCopy {
					return layout.Dimensions{}
				}
				size := min(gtx.Dp(24), gtx.Constraints.Max.X)
				return compactTextIcon(gtx, &b.action, image.Pt(size, gtx.Dp(24)), iconCopy, l.T("text.copy_code"))
			}))
		stack.Pop()
		pos.Y += header.Size.Y + gtx.Dp(4)
	}
	stack := op.Offset(pos).Push(gtx.Ops)
	dims := p.textFlow(inner, r, b, origin.Add(pos), animate)
	stack.Pop()
	pos.Y += dims.Size.Y
	size := image.Pt(gtx.Constraints.Max.X, pos.Y+gtx.Dp(6))
	call := macro.Stop()
	fillRounded(gtx, scheme(gtx).SurfaceVariant.Color.SetOpacity(.55), size, gtx.Dp(6))
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: size}
}

// Language is a label from untrusted input, never a filename or an instruction.
// Bound it before shaping and omit control/bidi characters in the header.
func codeLanguageLabel(language string) string {
	var out strings.Builder
	count := 0
	for _, r := range language {
		if count == 40 {
			break
		}
		count++
		if !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) {
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(out.String())
}

// quoteBlock reserves only a narrow right gutter for the disclosure icon.
// While shrinking, keep drawing the full text through the moving clip.
func (p *chatPage) quoteBlock(gtx layout.Context, r *messageRow, b *messageTextBlock, origin image.Point, l localization.Catalog, animate bool) layout.Dimensions {
	run := r.runs[b.first]
	pad := min(gtx.Dp(10), gtx.Constraints.Max.X/2)
	top := gtx.Dp(6)
	cell := 0
	if run.Collapsed {
		cell = min(gtx.Dp(24), max(0, gtx.Constraints.Max.X-2*pad))
	}
	inner := gtx
	inner.Constraints.Max.X = max(1, gtx.Constraints.Max.X-2*pad-cell)
	pos := image.Pt(pad, top)
	first := len(r.text.fragments)
	draw := func(limit int) (op.CallOp, layout.Dimensions) {
		b.maxLines = limit
		r.text.fragments = r.text.fragments[:first]
		macro := op.Record(gtx.Ops)
		stack := op.Offset(pos).Push(gtx.Ops)
		dims := p.textFlow(inner, r, b, origin.Add(pos), animate)
		stack.Pop()
		return macro.Stop(), dims
	}
	limit := 0
	if run.Collapsed && !b.expanded {
		limit = 3
	}
	call, dims := draw(limit)
	target := dims.Size.Y + 2*top
	size := image.Pt(gtx.Constraints.Max.X, b.height.Value(gtx, target, animate))
	if size.Y > target && limit > 0 {
		call, _ = draw(0)
	}
	visible := image.Rect(pad, top, max(pad, size.X-pad-cell), max(top, size.Y-top)).Add(origin)
	clipTextFragments(&r.text, first, visible)
	fillRounded(gtx, scheme(gtx).SurfaceVariant.Color.SetOpacity(.55), size, gtx.Dp(6))
	paint.FillShape(gtx.Ops, scheme(gtx).Primary.Color.AsNRGBA(), clip.UniformRRect(image.Rect(0, 0, min(size.X, gtx.Dp(3)), size.Y), gtx.Dp(1)).Op(gtx.Ops))
	area := clip.Rect(visible.Sub(origin)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	area.Pop()
	if cell > 0 {
		at := image.Pt(size.X-pad-cell, max(0, size.Y-top-cell))
		offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
			key, icon := "text.expand_quote", iconExpandMore
			if b.expanded {
				key, icon = "text.collapse_quote", iconExpandLess
			}
			return compactTextIcon(gtx, &b.action, image.Pt(cell, cell), icon, l.T(key))
		})
	}
	return layout.Dimensions{Size: size}
}

func compactTextIcon(gtx layout.Context, action *surface, size image.Point, icon wdk.IconWidget, label string) layout.Dimensions {
	col := scheme(gtx).Primary.Color
	return action.Layout(gtx, size, surfaceStyle{radius: gtx.Dp(4), background: col.SetOpacity(0), content: col, button: label}, func(gtx layout.Context) layout.Dimensions {
		px := min(gtx.Dp(16), size.X, size.Y)
		return offset(gtx, size.Sub(image.Pt(px, px)).Div(2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions { return icon(gtx, col) })
		})
	})
}

// Text selection uses model coordinates, not Gio's pointer clip. Prune it
// too so a hidden link or spoiler cannot be hit through the remaining area.
func clipTextFragments(text *textInteraction, first int, visible image.Rectangle) {
	fragments := text.fragments[first:]
	text.fragments = text.fragments[:first]
	for _, f := range fragments {
		f.Bounds = f.Bounds.Intersect(visible)
		if f.Bounds.Empty() {
			continue
		}
		clusters := f.Clusters[:0]
		for _, c := range f.Clusters {
			c.Bounds = c.Bounds.Intersect(visible)
			if !c.Bounds.Empty() {
				clusters = append(clusters, c)
			}
		}
		f.Clusters = clusters
		text.fragments = append(text.fragments, f)
	}
}
