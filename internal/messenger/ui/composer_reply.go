// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// replyBar is the strip over the composer that shows the message being
// replied to, as Telegram Desktop's field shows it: a click shows the
// message, the cross stops replying.
type replyBar struct {
	height       heightTransition
	chat         int64
	shown        *model.Message
	show, remove surface
}

// replyGap is the space between a floating composer and its reply strip.
const replyGap = 6

// replyTo makes what is sent next to chat a reply to m, and focuses the
// message field.
func (c *messageComposer) replyTo(gtx layout.Context, chat int64, m model.Message) {
	d := c.draft(chat)
	reply := m
	d.reply = &reply
	if !d.sending && c.source != nil {
		gtx.Execute(key.FocusCmd{Tag: &d.editor})
	}
	gtx.Execute(op.InvalidateCmd{})
}

// replyHeight is how much higher than its bar the composer of chat is for
// the reply strip, 0 when it replies to nothing.
func (c *messageComposer) replyHeight(gtx layout.Context, chat int64, classic bool) int {
	b := &c.replies
	if b.chat != chat {
		b.height = heightTransition{}
		b.shown = nil
		b.chat = chat
	}
	target := 0
	if d := c.drafts[chat]; d != nil && d.reply != nil {
		b.shown = d.reply
		target = gtx.Dp(44)
		if !classic {
			target += gtx.Dp(replyGap)
		}
	}
	height := b.height.Value(gtx, target, true)
	if height == 0 && target == 0 {
		b.shown = nil
	}
	return height
}

// replyLayout draws the reply strip of chat over bar, the composer's bar.
func (c *messageComposer) replyLayout(gtx layout.Context, chat int64, bar image.Rectangle, classic bool, backdrop *blurBackdrop, l localization.Catalog, p *chatPage) {
	d := c.draft(chat)
	height := c.replyHeight(gtx, chat, classic)
	if height == 0 || c.replies.shown == nil {
		return
	}
	reply := *c.replies.shown
	if d.reply == nil {
		gtx = gtx.Disabled()
	}
	if d.reply != nil && c.replies.remove.Clicked(gtx) {
		d.reply = nil
		gtx.Execute(op.InvalidateCmd{})
		height = c.replyHeight(gtx, chat, classic)
		gtx = gtx.Disabled()
	}
	if d.reply != nil && c.replies.show.Clicked(gtx) {
		p.jumpTo(reply.Key.MessageID)
	}
	rect := image.Rect(bar.Min.X, bar.Min.Y-height, bar.Max.X, bar.Min.Y)
	if !classic {
		rect.Max.Y = max(rect.Min.Y, rect.Max.Y-gtx.Dp(replyGap))
	}
	if rect.Empty() {
		return
	}
	inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		size := gtx.Constraints.Max
		radius := gtx.Dp(16)
		if classic {
			radius = 0
		}
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, backdrop, size, rect.Min, sc.SurfaceContainerHigh, radius)
		if classic {
			fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
		}
		name, text := p.replyNames(reply, l)
		close := min(gtx.Dp(44), size.X/3)
		inRect(gtx, image.Rect(0, 0, size.X-close, size.Y), func(gtx layout.Context) layout.Dimensions {
			area := gtx.Constraints.Max
			style := surfaceStyle{background: sc.Primary.Color.SetOpacity(0), content: sc.Primary.Color, button: l.Format("composer.reply_to", map[string]string{"name": name})}
			return c.replies.show.Layout(gtx, area, style, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 16, Right: 8, Top: 5, Bottom: 5}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Point{}
					fillRect(gtx, sc.Primary.Color, image.Pt(gtx.Dp(3), gtx.Constraints.Max.Y))
					return layout.Inset{Left: 11}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return label(gtx, l.Format("composer.reply_to", map[string]string{"name": name}), token.TypestyleLabelLargeEmphasized, sc.Primary.Color, 1)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return label(gtx, text, token.TypestyleBodySmall, sc.Surface.OnColor, 1)
							}),
						)
					})
				})
			})
		})
		inRect(gtx, image.Rect(size.X-close, 0, size.X, size.Y), func(gtx layout.Context) layout.Dimensions {
			area := gtx.Constraints.Max
			d := min(gtx.Dp(36), area.X, area.Y)
			circle := image.Rectangle{Max: image.Pt(d, d)}.Add(area.Sub(image.Pt(d, d)).Div(2))
			content := sc.SurfaceVariant.OnColor
			style := surfaceStyle{area: circle, radius: d / 2, background: content.SetOpacity(0), content: content, button: l.T("composer.reply_remove")}
			return c.replies.remove.Layout(gtx, area, style, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return iconClear(gtx, content) })
			})
		})
		return layout.Dimensions{Size: size}
	})
}

// replyNames are the name and the text a quote of m shows, as the quote
// over a reply in the history does.
func (p *chatPage) replyNames(m model.Message, l localization.Catalog) (name, text string) {
	name = m.SenderName
	if name == "" && m.Outgoing {
		name = l.T("history.you")
	}
	if name == "" {
		name = p.title
	}
	text = foundText(m, l)
	if m.SenderName != "" {
		text = strings.TrimPrefix(text, m.SenderName+": ")
	}
	return name, text
}
