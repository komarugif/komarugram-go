// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/unit"

	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/messenger/model"
)

// The marks of how far a message the account sent has gone: a clock while
// it is on its way, a check once Telegram has it, two once it was read.
var (
	iconSending  = wdk.RequireIconWidget(icons.DeviceAccessTime)
	iconSentMark = wdk.RequireIconWidget(icons.ActionDone)
	iconReadMark = wdk.RequireIconWidget(icons.ActionDoneAll)
)

// onlineColor is the dot on the avatar of a user online, Telegram
// Desktop's dialogsOnlineBadgeFg.
var onlineColor = token.NewMatColorFromHexRGB(0x4DC920)

// sendStateMarkSize is how large the marks are beside a message's time.
const sendStateMarkSize = unit.Dp(16)

// sendStateMark draws the mark of state in a square of size, in col;
// nothing for SendNone.
func sendStateMark(gtx layout.Context, state model.SendState, size unit.Dp, col token.MatColor) layout.Dimensions {
	var icon wdk.IconWidget
	switch state {
	case model.SendSending:
		icon = iconSending
	case model.SendSent:
		icon = iconSentMark
	case model.SendRead:
		icon = iconReadMark
	default:
		return layout.Dimensions{}
	}
	px := gtx.Dp(size)
	return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
		if state == model.SendSending {
			// The clock is drawn smaller, as the checks fill less of their
			// box than it does.
			inset := px / 8
			gtx.Constraints = layout.Exact(image.Pt(px-2*inset, px-2*inset))
			return offset(gtx, image.Pt(inset, inset), func(gtx layout.Context) layout.Dimensions { return icon(gtx, col) })
		}
		return icon(gtx, col)
	})
}

// sendState is the state the history marks m with: while the checks are
// hidden, only the clock of a message on its way, which always shows.
func (p *chatPage) sendState(gtx layout.Context, m model.Message) model.SendState {
	state := model.MessageSendState(m, p.kind, p.readOutbox)
	if calmOf(gtx)&calmRead != 0 && state != model.SendSending {
		return model.SendNone
	}
	return state
}

// previewSendState is the mark of a message of the previews of the
// settings: read, for one of the account's.
func previewSendState(gtx layout.Context, m model.Message) model.SendState {
	if calmOf(gtx)&calmRead != 0 || !m.Outgoing {
		return model.SendNone
	}
	return model.SendRead
}

// drawOnlineDot marks the avatar of size at at as a user's who is online:
// a dot at its lower right, in a ring of ring, the color around it.
func drawOnlineDot(gtx layout.Context, at image.Point, size int, ring token.MatColor) {
	outer, border := gtx.Dp(14), gtx.Dp(2)
	corner := at.Add(image.Pt(size-outer+gtx.Dp(1), size-outer+gtx.Dp(1)))
	offset(gtx, corner, func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, ring, image.Pt(outer, outer), outer/2)
		inner := outer - 2*border
		return offset(gtx, image.Pt(border, border), func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, onlineColor, image.Pt(inner, inner), inner/2)
			return layout.Dimensions{}
		})
	})
}

// rendererRevision is the RendererRevision of the history's layouts: the
// checks beside the time of the account's messages change where their text
// wraps, and 17 is that of the layouts made before there were any.
func rendererRevision(gtx layout.Context) uint32 {
	if calmOf(gtx)&calmRead != 0 {
		return 17
	}
	return 18
}
