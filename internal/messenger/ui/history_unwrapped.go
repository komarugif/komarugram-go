// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"
	"unicode/utf8"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/go-text/typesetting/segmenter"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// Stickers, video messages and a message of a single emoji are drawn
// without a bubble, over the chat's background, as the official clients
// draw them (Telegram Desktop's UnwrappedMedia and LargeEmoji). The time
// sits on a plate of its own beside the picture, or over its corner for the
// account's own; a reply or a forward goes in a small card at the side.
const (
	// stickerMax is the most a sticker takes on either side; a lone emoji
	// takes half of it.
	stickerMax     = unit.Dp(190)
	largeEmojiSize = stickerMax / 2
	// unwrappedSide is the widest the card at the side gets.
	unwrappedSide = unit.Dp(220)
)

// unwrapped reports whether m is drawn without a bubble. What needs a bubble
// to sit in — a caption, buttons, the comments bar — keeps it.
func (p *chatPage) unwrapped(m model.Message) bool {
	if len(m.Buttons) > 0 || m.Poll != nil || m.Todo != nil || m.WebPage != nil || len(m.Attachments) > 1 || p.showsComments(m) {
		return false
	}
	if m.Kind == model.MessageSticker || m.Kind == model.MessageRound {
		return m.Media != nil && m.Text == ""
	}
	_, _, ok := loneEmoji(m)
	return ok
}

// loneEmoji reports whether m is a text of one emoji, and returns it; a
// custom emoji's document comes as doc.
func loneEmoji(m model.Message) (emoji string, doc int64, ok bool) {
	if m.Kind != model.MessageText || m.Media != nil {
		return "", 0, false
	}
	text := strings.TrimSpace(m.Text)
	if text == "" || len(text) > 64 || !singleGrapheme(text) {
		return "", 0, false
	}
	// The only formatting a lone emoji may have is being a custom one.
	for _, e := range m.Entities {
		if e.Kind != "emoji" || e.DocumentID == 0 {
			return "", 0, false
		}
		doc = e.DocumentID
	}
	if len(m.Entities) > 1 {
		return "", 0, false
	}
	if doc == 0 && !isEmoji(text) {
		return "", 0, false
	}
	return text, doc, true
}

// singleGrapheme reports whether s is one user-perceived character: a family
// emoji or a flag is one, though made of several code points.
func singleGrapheme(s string) bool {
	var seg segmenter.Segmenter
	seg.Init([]rune(s))
	it := seg.GraphemeIterator()
	n := 0
	for it.Next() {
		if n++; n > 1 {
			return false
		}
	}
	return n == 1
}

// isEmoji reports whether the grapheme g is an emoji: a pictograph, a
// flag, a keycap, or a character asked to show as emoji (U+FE0F). Symbols
// that are emoji only on request, such as © or a digit, are text without it.
func isEmoji(g string) bool {
	if strings.ContainsRune(g, 0xFE0F) || strings.ContainsRune(g, 0x20E3) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(g)
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF: // pictographs, flags, emoticons
		return true
	case r >= 0x2600 && r <= 0x27BF: // ☀ ❤ ✅ and the like
		return true
	case r >= 0x2300 && r <= 0x23FF: // ⌚ ⏰ ⏳
		return true
	case r >= 0x2B00 && r <= 0x2BFF: // ⬛ ⭐
		return true
	case r == 0x3030 || r == 0x303D || r == 0x3297 || r == 0x3299:
		return true
	}
	return false
}

// unwrappedBody draws m, a message without a bubble.
func (p *chatPage) unwrappedBody(gtx layout.Context, r *messageRow, m model.Message, join bubbleJoin, l localization.Catalog, animate bool) layout.Dimensions {
	// The closures read the message from the row: captured, m itself would
	// be moved to the heap in each frame (see bubbleContent).
	r.shown = m
	msg := &r.shown
	picture := func(gtx layout.Context) layout.Dimensions {
		if msg.Kind == model.MessageSticker || msg.Kind == model.MessageRound {
			return p.mediaLayout(gtx, r, *msg, l, animate)
		}
		return p.largeEmoji(gtx, *msg, animate)
	}
	info := func(gtx layout.Context) layout.Dimensions { return infoPlate(gtx, *msg, l, p.sendState(gtx, *msg)) }
	side := p.unwrappedSide(msg, join, l, r)

	// The picture, with its time over the corner for the account's own.
	media := func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := picture(gtx)
		call := macro.Stop()
		call.Add(gtx.Ops)
		if !msg.Outgoing {
			return dims
		}
		imacro := op.Record(gtx.Ops)
		igtx := gtx
		igtx.Constraints.Min = image.Point{}
		idims := info(igtx)
		icall := imacro.Stop()
		offset(gtx, dims.Size.Sub(idims.Size).Sub(image.Pt(gtx.Dp(4), gtx.Dp(4))), func(gtx layout.Context) layout.Dimensions {
			icall.Add(gtx.Ops)
			return idims
		})
		return dims
	}
	column := []layout.FlexChild{layout.Rigid(media)}
	if len(msg.Reactions) > 0 {
		column = append(column, vspace(6), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.reactions(gtx, r, *msg, animate)
		}))
	}
	mediaColumn := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: alignFor(*msg)}.Layout(gtx, column...)
	}
	macro := op.Record(gtx.Ops)
	mdims := mediaColumn(gtx)
	mcall := macro.Stop()

	// The side: the reply or forward at its top, and for others' messages
	// the time at its bottom, level with the picture's.
	sideColumn := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unwrappedSide))
		gtx.Constraints.Min = image.Point{}
		var top layout.Dimensions
		if side != nil {
			top = side(gtx)
		}
		width, height := top.Size.X, top.Size.Y
		if !msg.Outgoing {
			imacro := op.Record(gtx.Ops)
			idims := info(gtx)
			icall := imacro.Stop()
			y := max(height+gtx.Dp(4), mdims.Size.Y-idims.Size.Y)
			if len(msg.Reactions) > 0 {
				// Level with the picture, not the reactions under it.
				y = max(height+gtx.Dp(4), mdims.Size.Y-idims.Size.Y-gtx.Dp(40))
			}
			offset(gtx, image.Pt(0, y), func(gtx layout.Context) layout.Dimensions {
				icall.Add(gtx.Ops)
				return idims
			})
			width, height = max(width, idims.Size.X), max(height, y+idims.Size.Y)
		}
		return layout.Dimensions{Size: image.Pt(width, height)}
	}
	gap := layout.Rigid(layout.Spacer{Width: 8}.Layout)
	mediaChild := layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		mcall.Add(gtx.Ops)
		return mdims
	})
	if msg.Outgoing {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx, layout.Rigid(sideColumn), gap, mediaChild)
	}
	return layout.Flex{Alignment: layout.Start}.Layout(gtx, mediaChild, gap, layout.Rigid(sideColumn))
}

func alignFor(m model.Message) layout.Alignment {
	if m.Outgoing {
		return layout.End
	}
	return layout.Start
}

// unwrappedSide is the card beside a message without a bubble: the sender
// in a group, where a forward came from and the message replied to; nil
// when there is none of them.
func (p *chatPage) unwrappedSide(m *model.Message, join bubbleJoin, l localization.Catalog, r *messageRow) layout.Widget {
	forward := !m.ForwardDate.IsZero()
	reply := m.ReplyToMessageID != 0 && m.ReplyToMessageID != p.threadRoot
	if !forward && !reply {
		return nil
	}
	return func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		var children []layout.FlexChild
		if m.SenderName != "" && !m.Outgoing && join&joinAbove == 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.layoutSenderName(gtx, m)
			}), vspace(3))
		}
		if forward {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				text := l.T("history.forward")
				if m.ForwardName != "" {
					text = l.Format("history.forwarded_from", map[string]string{"name": m.ForwardName, "user": m.ForwardName})
				}
				return label(gtx, text, token.TypestyleLabelMedium, sc.Primary.Color, 2)
			}))
		}
		if reply {
			if forward {
				children = append(children, vspace(4))
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.replyQuote(gtx, r, *m, l)
			}))
		}
		bubble := sc.Surface.Color
		if m.Outgoing {
			bubble = sc.PrimaryContainer.Color
		}
		macro := op.Record(gtx.Ops)
		dims := layout.UniformInset(6).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
		call := macro.Stop()
		fillRounded(gtx, bubble, dims.Size, gtx.Dp(12))
		call.Add(gtx.Ops)
		return dims
	}
}

// largeEmoji draws a lone emoji at half a sticker's size: a custom emoji as
// its animation, any other as text that big.
func (p *chatPage) largeEmoji(gtx layout.Context, m model.Message, animate bool) layout.Dimensions {
	emoji, doc, _ := loneEmoji(m)
	side := gtx.Dp(largeEmojiSize)
	size := image.Pt(side, side)
	if doc != 0 {
		if frame := customEmoji(p.media, p.images, doc, side, animate, scheme(gtx).Surface.OnColor.AsNRGBA()); frame != nil {
			return drawImage(gtx, p.images, frame, size)
		}
		return layout.Dimensions{Size: size}
	}
	theme := wdk.GetMaterialTheme(gtx)
	// A color emoji glyph is about as high as the text is big; a little
	// less keeps it inside the square with its line's room above and below.
	textSize := unit.Sp(float32(largeEmojiSize) * 0.8)
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	return exact(gtx, size, func(gtx layout.Context) layout.Dimensions {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			colorMacro := op.Record(gtx.Ops)
			paint.ColorOp{Color: scheme(gtx).Surface.OnColor.AsNRGBA()}.Add(gtx.Ops)
			color := colorMacro.Stop()
			// The theme's typeface, as the text of a message has it, falls
			// back to the color emoji font as that text does.
			face := font.Font{Typeface: theme.Typescale[token.TypestyleBodyLarge].Font}
			return widget.Label{MaxLines: 1}.Layout(gtx, theme.TextShaper, face, textSize, emoji, color)
		})
	})
}

// infoPlate is the time of a message without a bubble, on a plate that
// keeps it readable over any background, with its views, edit mark and the
// mark of state.
func infoPlate(gtx layout.Context, m model.Message, l localization.Catalog, state model.SendState) layout.Dimensions {
	sc := scheme(gtx)
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 2, Bottom: 2, Left: 7, Right: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return messageFooterIn(gtx, m, l, true, state, sc.InverseSurface.OnColor)
	})
	call := macro.Stop()
	fillRounded(gtx, sc.InverseSurface.Color.SetOpacity(0.55), dims.Size, dims.Size.Y/2)
	call.Add(gtx.Ops)
	return dims
}
