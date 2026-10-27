// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strconv"
	"time"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	// reactedPage is how many of who reacted a page asks for.
	reactedPage = 50
	// reactedTabs is how many tabs the dialog has at most: all reactions,
	// then the most chosen ones.
	reactedTabs = 5
	reactedRow  = 52
)

// reactedDialog lists who reacted to a message, as Telegram Desktop's
// reactions box does: a tab for all of them and one for each reaction.
type reactedDialog struct {
	modal   modal
	msg     model.Message
	filters []model.Reaction
	lists   []reactedList
	active  int
	tabs    tabRow
	close   surface
	retry   surface
	list    scroll.List
	loader  loadingIndicator
	// ctx ends the pages asked for when the dialog closes.
	ctx    context.Context
	cancel context.CancelFunc
}

// reactedList is who reacted, as far as its pages have come.
type reactedList struct {
	items   []model.Reacted
	rows    []surface
	next    string
	done    bool
	failed  bool
	loading bool
	results chan reactedResult
}

type reactedResult struct {
	page model.ReactedPage
	err  error
}

// open shows who reacted to m.
func (d *reactedDialog) open(p *chatPage, m model.Message) {
	d.stop()
	d.msg = m
	d.filters = []model.Reaction{{}}
	for _, r := range m.Reactions {
		if len(d.filters) == reactedTabs {
			break
		}
		if !r.Paid {
			d.filters = append(d.filters, model.Reaction{Emoji: r.Emoji, DocumentID: r.DocumentID, Count: r.Count})
		}
	}
	d.lists = make([]reactedList, len(d.filters))
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.modal.Open()
	p.invalidate()
}

func (d *reactedDialog) stop() {
	if d.cancel != nil {
		d.cancel()
	}
	*d = reactedDialog{}
}

// fetch asks for the next page of the active tab.
func (d *reactedDialog) fetch(p *chatPage) {
	lister, ok := p.source.(model.ReactionLister)
	l := &d.lists[d.active]
	if !ok || l.loading || l.done || l.failed {
		return
	}
	l.loading = true
	l.results = make(chan reactedResult, 1)
	results, msg, filter, offset, ctx := l.results, d.msg, d.filters[d.active], l.next, d.ctx
	go func() {
		defer crash.Recover("reacted", func(e *crash.Panic) { results <- reactedResult{err: e}; p.invalidate() })
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		page, err := lister.Reacted(ctx, msg, model.Reaction{Emoji: filter.Emoji, DocumentID: filter.DocumentID}, offset, reactedPage)
		results <- reactedResult{page, err}
		p.invalidate()
	}()
}

// update takes the pages that came; a page that did not is told in the
// dialog's toast.
func (d *reactedDialog) update(loc localization.Catalog) {
	for i := range d.lists {
		l := &d.lists[i]
		if l.results == nil {
			continue
		}
		select {
		case r := <-l.results:
			l.loading, l.results = false, nil
			if r.err != nil {
				l.failed = true
				d.modal.Toast(loc.T("reacted.failed"))
				continue
			}
			l.items = append(l.items, r.page.List...)
			l.next = r.page.Next
			l.done = r.page.Next == "" || len(r.page.List) == 0
		default:
		}
	}
}

// tabTitle draws the title of tab i: all of them, or the reaction, a
// custom emoji too, and how many chose it.
func (d *reactedDialog) tabTitle(gtx layout.Context, p *chatPage, i int, col token.MatColor, l localization.Catalog) layout.Dimensions {
	f := d.filters[i]
	if i == 0 {
		total := 0
		for _, r := range d.msg.Reactions {
			total += r.Count
		}
		return label(gtx, l.T("reacted.all")+" "+strconv.Itoa(total), token.TypestyleLabelLarge, col, 1)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.reactionIcon(gtx, f, p.animate)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(gtx.Dp(6), 0)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, strconv.Itoa(f.Count), token.TypestyleLabelLarge, col, 1)
		}),
	)
}

func (d *reactedDialog) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	d.update(l)
	if !d.modal.closing {
		if d.close.Clicked(gtx) {
			d.modal.Close()
		}
		if i, ok := d.tabs.Clicked(gtx, d.active, len(d.filters)); ok {
			d.active = i
			d.list = scroll.List{}
		}
	}
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(420))
		height := min(gtx.Constraints.Max.Y, gtx.Dp(560))
		gtx.Constraints = layout.Exact(image.Pt(width, height))
		// The tabs take the card's top edge to edge, as the composer's
		// picker has them, its rounded corners cutting their ripples.
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			pad := layout.Inset{Left: defaultCardPadding, Right: defaultCardPadding}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Dp(44)))
					return d.tabs.LayoutWith(gtx, len(d.filters), d.active, func(gtx layout.Context, i int, col token.MatColor) layout.Dimensions {
						return d.tabTitle(gtx, p, i, col, l)
					})
				}),
				vspace(8),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return pad.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						area := image.Rectangle{Max: gtx.Constraints.Max}
						d.tabs.Slide(gtx, area, func(gtx layout.Context) layout.Dimensions {
							return d.layoutList(gtx, p, l)
						})
						return layout.Dimensions{Size: area.Max}
					})
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: defaultCardPadding, Bottom: defaultCardPadding}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &d.close, l.T("stickers.close"))
						})
					})
				}),
			)
		}, 0)
	})
	if !shown {
		d.stop()
	}
}

// layoutList draws who reacted, as the active tab has them, and asks for
// more at its end.
func (d *reactedDialog) layoutList(gtx layout.Context, p *chatPage, l localization.Catalog) layout.Dimensions {
	list := &d.lists[d.active]
	if len(list.items) == 0 {
		if list.failed {
			if d.retry.Clicked(gtx) {
				list.failed = false
				d.fetch(p)
			}
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.retry, l.T("history.retry")) })
		}
		d.fetch(p)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return d.loader.sized(gtx, l, 32)
		})
	}
	d.list.Axis = layout.Vertical
	n := len(list.items)
	for len(list.rows) < n {
		list.rows = append(list.rows, surface{})
	}
	more := !list.done && !list.failed
	count := n
	if more {
		count++
	}
	dims := d.list.Layout(gtx, count, func(gtx layout.Context, i int) layout.Dimensions {
		if i == n {
			d.fetch(p)
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return d.loader.sized(gtx, l, 24)
				})
			})
		}
		return d.row(gtx, p, &list.rows[i], list.items[i])
	})
	return dims
}

// row draws one who reacted: their avatar and name, and their reaction. A
// click opens their chat, as a group member's avatar does, or their info
// when the account has no chat with them, and closes the dialog.
func (d *reactedDialog) row(gtx layout.Context, p *chatPage, s *surface, r model.Reacted) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(reactedRow))
	if p.openSender == nil || r.PeerID == 0 {
		return d.rowContent(gtx, p, size, r)
	}
	if s.Clicked(gtx) && !d.modal.closing {
		p.openSender(reactedSender(d.msg, r))
		d.modal.Close()
	}
	style := surfaceStyle{radius: gtx.Dp(12), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		return d.rowContent(gtx, p, size, r)
	})
}

// reactedSender is m as sent by r, who reacted to it, which the store's
// SenderChat asks for by m, as forwardSender does for whom m came from.
func reactedSender(m model.Message, r model.Reacted) model.Message {
	m.SenderID, m.SenderName = r.PeerID, r.Name
	m.Outgoing = false
	m.ForwardPostID, m.ForwardFromID = 0, 0
	return m
}

func (d *reactedDialog) rowContent(gtx layout.Context, p *chatPage, size image.Point, r model.Reacted) layout.Dimensions {
	sc := scheme(gtx)
	const avatarSize = 36
	offset(gtx, image.Pt(gtx.Dp(4), (size.Y-gtx.Dp(avatarSize))/2), func(gtx layout.Context) layout.Dimensions {
		kind := model.KindUser
		if r.PeerID < 0 {
			kind = model.KindGroup
		}
		if p.avatar != nil {
			return p.avatar(gtx, r.PeerID, kind, r.Name, avatarSize)
		}
		return avatar(gtx, r.PeerID, kind, r.Name, avatarSize)
	})
	iconPx := gtx.Dp(24)
	textX := gtx.Dp(4 + avatarSize + 12)
	textGtx := gtx
	textGtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-textX-iconPx-gtx.Dp(8), 0), size.Y)}
	offset(textGtx, image.Pt(textX, (size.Y-gtx.Sp(20))/2), func(gtx layout.Context) layout.Dimensions {
		return label(gtx, r.Name, token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
	})
	offset(gtx, image.Pt(size.X-iconPx-gtx.Dp(4), (size.Y-iconPx)/2), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(iconPx, iconPx))
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return p.reactionIcon(gtx, r.Reaction, p.animate)
		})
	})
	return layout.Dimensions{Size: size}
}
