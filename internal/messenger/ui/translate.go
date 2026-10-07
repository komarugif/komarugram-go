// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"time"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// translateDialog shows a message, or text selected in one, and its
// translation to the language of the interface, as Telegram Desktop's
// translate box does.
type translateDialog struct {
	modal   modal
	text    string
	result  string
	failed  bool
	loaded  bool
	results chan translateResult
	cancel  context.CancelFunc
	close   surface
	retry   surface
	// start asks for the translation again, after a failure.
	start  func()
	list   scroll.List
	loader loadingIndicator
}

type translateResult struct {
	text string
	err  error
}

// open translates message m, or text selected in it when selected is set.
func (d *translateDialog) open(p *chatPage, m model.Message, selected string, to string) {
	d.stop()
	translator, ok := p.source.(model.Translator)
	if !ok {
		return
	}
	d.text = menuText(m)
	id := m.Key.MessageID
	if selected != "" {
		d.text, id = selected, 0
	}
	d.modal.Open()
	chat, text := m.Key.ChatID, d.text
	d.start = func() {
		d.loaded, d.failed = false, false
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		d.cancel = cancel
		d.results = make(chan translateResult, 1)
		results := d.results
		go func() {
			defer cancel()
			out, err := translator.Translate(ctx, chat, id, text, to)
			results <- translateResult{out, err}
			p.invalidate()
		}()
	}
	d.start()
}

func (d *translateDialog) stop() {
	if d.cancel != nil {
		d.cancel()
	}
	*d = translateDialog{}
}

func (d *translateDialog) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	select {
	case r := <-d.results:
		d.loaded, d.result, d.failed = true, r.text, r.err != nil
		if r.err != nil {
			d.modal.Toast(l.T("translate.failed"))
		}
	default:
	}
	if d.failed && d.retry.Clicked(gtx) {
		d.start()
	}
	if !d.modal.closing && d.close.Clicked(gtx) {
		d.modal.Close()
	}
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(460))
		height := min(gtx.Constraints.Max.Y, gtx.Dp(520))
		gtx.Constraints = layout.Exact(image.Pt(width, height))
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("translate.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, d.text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 4)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("translate.to"), token.TypestyleTitleSmall, sc.Primary.Color, 1)
				}),
				vspace(4),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					switch {
					case !d.loaded:
						return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.loader.sized(gtx, l, 28) })
					case d.failed:
						return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.retry, l.T("history.retry")) })
					}
					d.list.Axis = layout.Vertical
					return d.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
						return label(gtx, d.result, token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
					})
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return textButton(gtx, &d.close, l.T("stickers.close"))
					})
				}),
			)
		}, defaultCardPadding)
	})
	if !shown {
		d.stop()
	}
}
