// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"strconv"
	"strings"

	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	// pinnedBarHeight is the height of the pinned message bar, under the
	// chat's header.
	pinnedBarHeight = unit.Dp(52)
	// pinnedSegments is how many segments the bar's line shows at most, one
	// for each pinned message, as Telegram Desktop's does.
	pinnedSegments = 4
)

// pinnedBar shows a pinned message of the chat over its history, as
// Telegram Desktop's does (HistoryWidget::updatePinnedViewer): the latest
// one above the bottom of what the history shows. A click goes to it and
// then shows the one before; a click on the oldest shows the latest again.
// Scrolling down forgets the clicks.
type pinnedBar struct {
	chat int64
	// clicked is the pinned message a click went to last.
	clicked model.MessageID
	// bottom is the message at the bottom of the history in the last frame.
	bottom model.MessageID
	bar    surface
	hide   *button.Button
}

// pinnedShown is the pinned message of ids to show when the history shows
// bottom at its bottom and clicked was the pinned message clicked last,
// and its index among them.
func pinnedShown(ids []model.MessageID, bottom, clicked model.MessageID) (model.MessageID, int) {
	if len(ids) == 0 {
		return 0, 0
	}
	around := model.MessageID(1<<31 - 1)
	if bottom != 0 {
		// The message at the bottom counts as above it.
		around = bottom + 1
	}
	if clicked != 0 {
		if clicked <= ids[0] {
			// After the oldest, the latest again.
			around = model.MessageID(1<<31 - 1)
		} else {
			around = min(around, clicked)
		}
	}
	i, _ := slices.BinarySearch(ids, around)
	if i == 0 {
		return ids[0], 0
	}
	return ids[i-1], i - 1
}

// pinnedIDs are the pinned messages of the open chat, if its store knows
// them.
func (p *chatPage) pinnedIDs(chat int64) []model.MessageID {
	if s, ok := p.source.(model.PinnedSource); ok && p.threadRoot == 0 {
		return s.PinnedMessages(chat)
	}
	return nil
}

// pinnedHeight is how much of the page's top the pinned bar takes.
func (p *chatPage) pinnedHeight(gtx layout.Context, chat int64) int {
	if len(p.pinnedIDs(chat)) == 0 {
		return 0
	}
	return gtx.Dp(pinnedBarHeight)
}

// bottomMessage is the message at the bottom of what the history shows.
func (p *chatPage) bottomMessage() model.MessageID {
	last := p.list.Position.First + max(p.list.Position.Count, 1) - 1
	if !p.restored || last < 0 || last >= len(p.messages) {
		return 0
	}
	// Drafts bots stream are not messages to read.
	for last > 0 && p.messages[last].Streaming {
		last--
	}
	return p.messages[last].Key.MessageID
}

// layoutPinned draws the pinned bar across the top of gtx.
func (p *chatPage) layoutPinned(gtx layout.Context, chat int64, l localization.Catalog) {
	b := &p.pinned
	if b.chat != chat {
		*b = pinnedBar{chat: chat, hide: b.hide}
	}
	if b.hide == nil {
		b.hide = button.Text()
	}
	ids := p.pinnedIDs(chat)
	if len(ids) == 0 {
		return
	}
	bottom := p.bottomMessage()
	if bottom != 0 && b.bottom != 0 && bottom > b.bottom {
		// Scrolled down: clicks are forgotten.
		b.clicked = 0
	}
	if bottom != 0 {
		b.bottom = bottom
	}
	if b.clicked != 0 && bottom != 0 && bottom < b.clicked {
		// Scrolled up past the message clicked.
		b.clicked = 0
	}
	shown, index := pinnedShown(ids, b.bottom, b.clicked)
	if b.hide.Clicked(gtx) {
		if s, ok := p.source.(model.PinnedSource); ok {
			s.HidePinned(chat)
		}
		return
	}
	if b.bar.Clicked(gtx) {
		b.clicked = shown
		p.jumpTo(shown)
		shown, index = pinnedShown(ids, b.bottom, b.clicked)
	}

	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(pinnedBarHeight))
	fillRect(gtx, sc.Surface.Color, size)
	gtx.Constraints = layout.Exact(size)
	b.bar.Layout(gtx, size, surfaceStyle{content: sc.Surface.OnColor}, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: size}
	})
	title := l.T("pinned.title")
	switch {
	case index+1 >= len(ids):
	case len(ids) == 2:
		title = l.T("pinned.previous")
	default:
		title += " #" + strconv.Itoa(index+1)
	}
	m, state, _ := p.referenced(chat, shown)
	text := l.T("service.loading")
	switch state {
	case model.LookupFound:
		text = p.pinnedText(m, l)
	case model.LookupGone:
		text = l.T("service.deleted_message")
	}
	hidePx := gtx.Dp(48)
	pad := gtx.Dp(16)
	lineX, lineH := pad, gtx.Dp(36)
	offset(gtx, image.Pt(lineX, (size.Y-lineH)/2), func(gtx layout.Context) layout.Dimensions {
		pinnedLine(gtx, gtx.Dp(3), lineH, index, len(ids))
		return layout.Dimensions{}
	})
	textX := lineX + gtx.Dp(3) + gtx.Dp(10)
	textGtx := gtx
	textGtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-textX-hidePx-gtx.Dp(4), 0), size.Y)}
	offset(textGtx, image.Pt(textX, gtx.Dp(7)), func(gtx layout.Context) layout.Dimensions {
		return label(gtx, title, token.TypestyleLabelLargeEmphasized, sc.Primary.Color, 1)
	})
	offset(textGtx, image.Pt(textX, gtx.Dp(27)), func(gtx layout.Context) layout.Dimensions {
		return label(gtx, text, token.TypestyleBodyMedium, sc.Surface.OnColor, 1)
	})
	offset(gtx, image.Pt(size.X-hidePx-gtx.Dp(4), (size.Y-hidePx)/2), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(hidePx, hidePx))
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return b.hide.LayoutIconOnly(gtx, l.T("pinned.hide"), iconClear)
		})
	})
	offset(gtx, image.Pt(0, size.Y-gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
		return layout.Dimensions{}
	})
}

// pinnedText is a pinned message's words on the bar: its text, or what
// its media is.
func (p *chatPage) pinnedText(m model.Message, l localization.Catalog) string {
	if m.Kind == model.MessageService {
		return p.serviceText(m, l)
	}
	if m.Poll != nil && strings.TrimSpace(m.Text) == "" {
		return m.Poll.Question
	}
	text := foundText(m, l)
	if m.SenderName != "" {
		text = strings.TrimPrefix(text, m.SenderName+": ")
	}
	return text
}

// pinnedLine draws the bar's line: a segment for each pinned message, up
// to pinnedSegments of them, with the one shown filled.
func pinnedLine(gtx layout.Context, width, height, index, count int) {
	sc := scheme(gtx)
	if count <= 1 {
		fillRounded(gtx, sc.Primary.Color, image.Pt(width, height), width/2)
		return
	}
	segments := min(count, pinnedSegments)
	gap := gtx.Dp(2)
	seg := (height - gap*(segments-1)) / segments
	// The window of segments follows the one shown, as the line scrolls in
	// Telegram Desktop.
	first := min(max(index-segments/2, 0), count-segments)
	for i := range segments {
		col := sc.Primary.Color.SetOpacity(.35)
		if first+i == index {
			col = sc.Primary.Color
		}
		// The latest is at the bottom.
		y := i * (seg + gap)
		offset(gtx, image.Pt(0, y), func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, col, image.Pt(width, seg), width/2)
			return layout.Dimensions{}
		})
	}
}
