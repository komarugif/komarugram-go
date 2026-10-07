// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// sendConfirm asks before a sticker or a GIF is sent, as AyuGram's
// confirmations do.
type sendConfirm struct {
	modal        modal
	tab          model.PickerTab
	item         model.PickerItem
	send, cancel surface
}

// asksConfirmation reports whether what tab sends waits for a
// confirmation.
func (c *messageComposer) asksConfirmation(tab model.PickerTab) bool {
	if c.confirmations == nil {
		return false
	}
	sticker, gif := c.confirmations()
	return tab == model.PickerStickers && sticker || tab == model.PickerGIF && gif
}

func (s *sendConfirm) ask(tab model.PickerTab, item model.PickerItem) {
	s.tab, s.item = tab, item
	s.modal.Open()
}

// layoutConfirm draws the question over the page, and sends what was
// chosen once it is confirmed.
func (c *messageComposer) layoutConfirm(gtx layout.Context, p *chatPage, l localization.Catalog) {
	s := &c.sendConfirm
	if !s.modal.Shown() {
		return
	}
	if !s.modal.closing {
		if s.cancel.Clicked(gtx) {
			s.modal.Close()
		}
		if s.send.Clicked(gtx) {
			item := s.item
			c.submit(c.chat, model.OutgoingMessage{Item: &item})
			s.modal.Close()
		}
	}
	question := l.T("confirm.sticker")
	if s.tab == model.PickerGIF {
		question = l.T("confirm.gif")
	}
	s.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return s.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, question, token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return p.confirmPreview(gtx, s.item)
					})
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &s.cancel, l.T("history.cancel")) }),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &s.send, l.T("confirm.send")) }),
						)
					})
				}),
			)
		}, defaultCardPadding)
	})
}

// confirmPreview draws what is about to be sent, as its picker shows it.
func (p *chatPage) confirmPreview(gtx layout.Context, item model.PickerItem) layout.Dimensions {
	size := image.Pt(gtx.Dp(160), gtx.Dp(160))
	if item.Media.Media == nil || p.media == nil || p.images == nil {
		return layout.Dimensions{Size: size}
	}
	frame, _ := p.media.Frame(item.Media, p.animate)
	if frame == nil {
		return layout.Dimensions{Size: size}
	}
	return exact(gtx, size, func(gtx layout.Context) layout.Dimensions {
		return widget.Image{Src: p.images.Op(frame), Fit: widget.Contain}.Layout(gtx)
	})
}
