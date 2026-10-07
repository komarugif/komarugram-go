// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"komarugram/internal/messenger/model"
)

// The strip of reactions at the top of a message's menu, as Telegram
// Desktop's selector has it: one row, which a button expands into all of
// them.
const (
	reactionCell     = 36
	reactionsPerRow  = 7
	reactionRowsShow = 5
	reactionPadding  = 6
)

// reactionStrip is the reactions part of the message menu.
type reactionStrip struct {
	shown    []model.Reaction
	cells    []surface
	expand   surface
	expanded bool
	list     widget.List
}

// canReact reports whether the account may react to m in the open chat.
func (p *chatPage) canReact(m model.Message) bool {
	return m.Kind != model.MessageService && m.Key.MessageID > 0 && !m.Deleted && !p.frozen.Frozen()
}

// menuReactions are the reactions the menu of m offers, with the chosen ones
// marked; none when the chat allows none or they have not loaded.
func (p *chatPage) menuReactions(m model.Message) []model.Reaction {
	reactor, ok := p.source.(model.Reactor)
	if !ok || !p.canReact(m) {
		return nil
	}
	available, _, ok := reactor.ChatReactions(p.chat)
	if !ok {
		return nil
	}
	out := make([]model.Reaction, len(available))
	for i, r := range available {
		out[i] = model.Reaction{Emoji: r.Emoji, DocumentID: r.DocumentID}
		for _, mine := range m.Reactions {
			if mine.Same(r) && mine.Chosen {
				out[i].Chosen = true
			}
		}
	}
	return out
}

// rows is how many rows of cells the strip shows.
func (s *reactionStrip) rows() int {
	if len(s.shown) == 0 {
		return 0
	}
	if !s.expanded {
		return 1
	}
	return min(reactionRowsShow, (len(s.shown)+reactionsPerRow-1)/reactionsPerRow)
}

// height is the strip's height, with the line that sets it apart from the
// actions; 0 when it has nothing to show.
func (s *reactionStrip) height(gtx layout.Context) int {
	rows := s.rows()
	if rows == 0 {
		return 0
	}
	return rows*gtx.Dp(reactionCell) + 2*gtx.Dp(reactionPadding) + gtx.Dp(9)
}

// collapsedCount is how many reactions the collapsed row has room for: all
// of them, or one less than a row with the expand button.
func (s *reactionStrip) collapsedCount() int {
	if len(s.shown) <= reactionsPerRow {
		return len(s.shown)
	}
	return reactionsPerRow - 1
}

// update returns the reaction clicked, if any, and flips the strip open or
// closed on the expand button.
func (s *reactionStrip) update(gtx layout.Context) (model.Reaction, bool) {
	s.grow()
	if s.expand.Clicked(gtx) {
		s.expanded = !s.expanded
		gtx.Execute(op.InvalidateCmd{})
	}
	for i, r := range s.shown {
		if s.cells[i].Clicked(gtx) {
			return r, true
		}
	}
	return model.Reaction{}, false
}

// grow makes a cell for every reaction shown. The reactions come after the
// menu opened, when they load, so the frame that first shows them may not
// have updated the strip yet.
func (s *reactionStrip) grow() {
	for len(s.cells) < len(s.shown) {
		s.cells = append(s.cells, surface{})
	}
}

// layout draws the strip at the top of a menu of width, with the line under
// it.
func (s *reactionStrip) layout(gtx layout.Context, p *chatPage, width int, animate bool) {
	s.layoutHeight(gtx, p, width, s.height(gtx), animate)
}

func (s *reactionStrip) layoutHeight(gtx layout.Context, p *chatPage, width, height int, animate bool) {
	defer clip.Rect(image.Rect(0, 0, width, height)).Push(gtx.Ops).Pop()
	s.grow()
	sc := scheme(gtx)
	cell := gtx.Dp(reactionCell)
	pad := gtx.Dp(reactionPadding)
	x0 := max(pad, (width-reactionsPerRow*cell)/2)
	drawCell := func(gtx layout.Context, i int) layout.Dimensions {
		r := s.shown[i]
		background := sc.Surface.OnColor.SetOpacity(0)
		if r.Chosen {
			background = sc.SecondaryContainer.Color
		}
		size := image.Pt(cell, cell)
		style := surfaceStyle{radius: cell / 2, background: background, content: sc.Surface.OnColor, button: r.Emoji}
		return s.cells[i].Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(size)
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.reactionIcon(gtx, r, animate)
			})
		})
	}
	rows := s.rows()
	if !s.expanded && height <= s.height(gtx) {
		count := s.collapsedCount()
		for i := range count {
			offset(gtx, image.Pt(x0+i*cell, pad), func(gtx layout.Context) layout.Dimensions { return drawCell(gtx, i) })
		}
		if count < len(s.shown) {
			s.layoutExpand(gtx, image.Pt(x0+count*cell, pad), cell, iconExpandMore)
		}
	} else {
		all := (len(s.shown) + reactionsPerRow - 1) / reactionsPerRow
		rows = min(reactionRowsShow, all)
		s.list.Axis = layout.Vertical
		inRect(gtx, image.Rect(0, pad, width, min(pad+rows*cell, max(pad, height-pad-gtx.Dp(9)))), func(gtx layout.Context) layout.Dimensions {
			return s.list.List.Layout(gtx, all, func(gtx layout.Context, row int) layout.Dimensions {
				for col := range reactionsPerRow {
					i := row*reactionsPerRow + col
					if i >= len(s.shown) {
						break
					}
					offset(gtx, image.Pt(x0+col*cell, 0), func(gtx layout.Context) layout.Dimensions { return drawCell(gtx, i) })
				}
				return layout.Dimensions{Size: image.Pt(width, cell)}
			})
		})
	}
	line := op.Offset(image.Pt(0, max(0, height-gtx.Dp(5)))).Push(gtx.Ops)
	fillRect(gtx, sc.OutlineVariant, image.Pt(width, gtx.Dp(1)))
	line.Pop()
}

func (s *reactionStrip) layoutExpand(gtx layout.Context, at image.Point, cell int, icon wdk.IconWidget) {
	sc := scheme(gtx)
	offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(cell, cell)
		style := surfaceStyle{radius: cell / 2, background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: "…"}
		return s.expand.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
			px := gtx.Dp(24)
			defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
			return offset(gtx, image.Pt((cell-px)/2, (cell-px)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
					return icon(gtx, sc.SurfaceVariant.OnColor)
				})
			})
		})
	})
}

// quickReact puts the account's default reaction on m, or takes it back,
// as a double click does in Telegram Desktop.
func (p *chatPage) quickReact(m model.Message) {
	quick, ok := p.source.(model.QuickReactor)
	reactor, canToggle := p.source.(model.Reactor)
	if !ok || !canToggle || !p.canReact(m) {
		return
	}
	if r, ok := quick.QuickReaction(p.chat); ok {
		reactor.ToggleReaction(m, r, p.reportMedia)
		p.invalidate()
	}
}
