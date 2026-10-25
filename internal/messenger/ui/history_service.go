// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// serviceText words a service message of the open chat, a call included.
func (p *chatPage) serviceText(m model.Message, l localization.Catalog) string {
	ctx := localization.ServiceContext{Chat: p.title, Channel: p.kind == model.KindChannel}
	if m.Service != nil && m.Service.Kind == model.ServicePin && m.ReplyToMessageID != 0 {
		switch pinned, state, _ := p.referenced(m.Key.ChatID, m.ReplyToMessageID); state {
		case model.LookupFound:
			ctx.Pinned = &pinned
		case model.LookupGone:
			ctx.PinnedGone = true
		}
	}
	return l.Service(m, ctx)
}

// servicePill draws a service message as Telegram does: its words on a
// plate across the middle of the history. A pin's plate shows the message
// pinned.
func (p *chatPage) servicePill(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) layout.Dimensions {
	txt := p.serviceText(m, l)
	if txt == "" {
		return layout.Dimensions{}
	}
	sc := scheme(gtx)
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(480))
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 5, Bottom: 5, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return centeredLabel(gtx, txt, token.TypestyleLabelLarge, sc.SecondaryContainer.OnColor, 0)
	})
	call := macro.Stop()
	radius := min(dims.Size.Y/2, gtx.Dp(14))
	plate := func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, sc.SecondaryContainer.Color, dims.Size, radius)
		call.Add(gtx.Ops)
		return dims
	}
	pin := m.Service != nil && m.Service.Kind == model.ServicePin && m.ReplyToMessageID != 0
	// A mention in a story opens it, as Telegram Desktop's View Story.
	story := m.Service != nil && m.Service.Kind == model.ServiceStoryMention && m.StoryShown()
	if !pin && !story {
		return plate(gtx)
	}
	style := surfaceStyle{radius: radius, content: sc.SecondaryContainer.OnColor}
	return r.reply.Layout(gtx, dims.Size, style, func(gtx layout.Context) layout.Dimensions {
		return plate(gtx)
	})
}
