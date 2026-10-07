// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"sync"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
)

// emojiPacksDialog lists the sets of a message's custom emoji, as Telegram
// Desktop's StickersBox for them does; a set opens in the sticker set
// dialog.
type emojiPacksDialog struct {
	modal   modal
	refs    []model.StickerSetRef
	sets    []model.StickerSet
	loaded  []bool
	results chan emojiPackResult
	cancel  context.CancelFunc
	rows    []surface
	close   surface
	list    scroll.List
	loader  loadingIndicator
}

type emojiPackResult struct {
	index int
	set   model.StickerSet
	err   error
}

func (d *emojiPacksDialog) stop() {
	if d.cancel != nil {
		d.cancel()
	}
	*d = emojiPacksDialog{}
}

// open shows refs, whose names and sizes load in the background.
func (d *emojiPacksDialog) open(p *chatPage, refs []model.StickerSetRef) {
	d.stop()
	d.refs = refs
	d.sets = make([]model.StickerSet, len(refs))
	d.loaded = make([]bool, len(refs))
	d.rows = make([]surface, len(refs))
	d.modal.Open()
	source, ok := p.source.(model.StickerSetStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	d.cancel = cancel
	d.results = make(chan emojiPackResult, len(refs))
	results := d.results
	go func() {
		defer cancel()
		var wg sync.WaitGroup
		limit := make(chan struct{}, 4)
		for i, ref := range refs {
			wg.Go(func() {
				limit <- struct{}{}
				defer func() { <-limit }()
				set, err := source.StickerSet(ctx, ref)
				results <- emojiPackResult{index: i, set: set, err: err}
				p.invalidate()
			})
		}
		wg.Wait()
	}()
	p.invalidate()
}

func (d *emojiPacksDialog) update() {
	for d.results != nil {
		select {
		case r := <-d.results:
			if r.index < len(d.sets) {
				d.loaded[r.index] = true
				if r.err == nil {
					d.sets[r.index] = r.set
				}
			}
		default:
			return
		}
	}
}

func (d *emojiPacksDialog) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	d.update()
	if !d.modal.closing {
		if d.close.Clicked(gtx) {
			d.modal.Close()
		}
		for i := range d.rows {
			if d.rows[i].Clicked(gtx) {
				d.modal.Close()
				p.stickers.open(p, d.refs[i])
				break
			}
		}
	}
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(420))
		height := min(gtx.Constraints.Max.Y, gtx.Dp(140)+min(len(d.refs), 6)*gtx.Dp(56))
		gtx.Constraints = layout.Exact(image.Pt(width, height))
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("emoji_packs.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 2)
				}),
				vspace(12),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					d.list.Axis = layout.Vertical
					return d.list.Layout(gtx, len(d.refs), func(gtx layout.Context, i int) layout.Dimensions {
						return d.row(gtx, i, l)
					})
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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

// row draws set i: its name and size once loaded.
func (d *emojiPacksDialog) row(gtx layout.Context, i int, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	set := d.sets[i]
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(56))
	style := surfaceStyle{radius: gtx.Dp(12), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: set.Title}
	return d.rows[i].Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				if !d.loaded[i] {
					return d.loader.sized(gtx, l, 20)
				}
				if set.Title == "" {
					// The row has no name to show for a set that did not
					// load: this stands in for it, not as an error.
					return label(gtx, l.T("emoji_packs.failed"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, set.Title, token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.Count("stickers.emoji_count", set.Count, nil), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
					}),
				)
			})
		})
	})
}
