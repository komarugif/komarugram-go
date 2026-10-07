// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"io"
	"strconv"
	"strings"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/widget"
)

type stickerSetResult struct {
	// cached marks the set as last fetched, shown while it is fetched again.
	cached    bool
	author    *model.Chat
	authorID  int64
	pack      *model.StickerSet
	installed *bool
	exported  bool
	path      string
	err       error
}

type stickerSetDialog struct {
	modal modal
	hover hoverPlay
	ref   model.StickerSetRef
	pack  *model.StickerSet
	chat  int64
	busy  bool
	// refreshing is set while a set shown from the cache is fetched again.
	refreshing bool
	// err is why the set did not load; it has no pack then.
	err                    error
	results                chan stickerSetResult
	cancel                 context.CancelFunc
	list                   scroll.List
	close, action, retry   surface
	more, download, author surface
	authorResult           *stickerSetResult
	menu                   contextMenu
	menuOpen               bool
	menuRect               image.Rectangle
	menuDismiss            struct{}
	menuPanel              struct{}
	chooseArchive          func(context.Context, string) (string, error)
	cells                  []surface
	loader                 loadingIndicator
}

func (d *stickerSetDialog) stop() {
	if d.cancel != nil {
		d.cancel()
	}
	*d = stickerSetDialog{}
}

func (d *stickerSetDialog) open(p *chatPage, ref model.StickerSetRef) {
	d.stop()
	d.ref, d.chat = ref, p.chat
	d.modal.Open()
	p.stickerViewShown()
	d.fetch(p)
	p.invalidate()
}

func (d *stickerSetDialog) fetch(p *chatPage) {
	source, ok := p.source.(model.StickerSetStore)
	if !ok {
		d.err = errors.New("sticker set unavailable")
		return
	}
	if d.cancel != nil {
		d.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	d.cancel = cancel
	d.busy, d.err = true, nil
	d.results = make(chan stickerSetResult, 2)
	results, ref := d.results, d.ref
	cache, _ := p.source.(model.StickerSetCache)
	go func() {
		// The set as last fetched shows at once, with the first frames of its
		// stickers still in the media cache; the fetch then brings it up to
		// date.
		if cache != nil {
			if pack, ok := cache.CachedStickerSet(ctx, ref); ok {
				results <- stickerSetResult{cached: true, pack: &pack}
				p.invalidate()
			}
		}
		pack, err := source.StickerSet(ctx, ref)
		results <- stickerSetResult{pack: &pack, err: err}
		p.invalidate()
	}()
}

func (d *stickerSetDialog) change(p *chatPage, installed bool) {
	source, ok := p.source.(model.StickerSetStore)
	if !ok || d.pack == nil {
		return
	}
	d.stopRefresh()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	d.cancel = cancel
	d.busy, d.err = true, nil
	d.results = make(chan stickerSetResult, 1)
	results, ref := d.results, d.pack.Ref
	go func() {
		err := source.SetStickerSetInstalled(ctx, ref, installed)
		results <- stickerSetResult{installed: &installed, err: err}
		p.invalidate()
	}()
}

func (d *stickerSetDialog) export(p *chatPage) {
	if d.pack == nil || d.busy {
		return
	}
	d.stopRefresh()
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.busy, d.err = true, nil
	d.results = make(chan stickerSetResult, 1)
	results, source := d.results, p.source
	pack := *d.pack
	pack.Items = append([]model.PickerItem(nil), pack.Items...)
	choose := d.chooseArchive
	if choose == nil {
		choose = chooseStickerArchive
	}
	go func() {
		path, err := choose(ctx, pack.Title)
		if err == nil && path != "" {
			err = writeStickerSetArchive(ctx, source, pack, path)
		}
		results <- stickerSetResult{exported: true, path: path, err: err}
		p.invalidate()
	}()
}

func (d *stickerSetDialog) back() bool {
	if !d.menuOpen {
		return false
	}
	d.menuOpen = false
	return true
}

// update takes the results of what the dialog asked for; the outcome of an
// action, or its failure, goes in the dialog's toast.
func (d *stickerSetDialog) update(l localization.Catalog) {
	if d.results == nil {
		return
	}
	select {
	case result := <-d.results:
		if result.cached {
			d.setPack(result.pack)
			d.busy, d.refreshing = false, true
			return
		}
		refreshing := d.refreshing
		d.busy, d.results, d.refreshing = false, nil, false
		if refreshing && result.err != nil {
			// Offline, or the fetch failed: the set shown stays.
			if d.cancel != nil {
				d.cancel()
				d.cancel = nil
			}
			return
		}
		if d.cancel != nil {
			d.cancel()
			d.cancel = nil
		}
		if result.authorID != 0 {
			d.authorResult = &result
			return
		}
		if result.exported {
			if result.err != nil {
				d.modal.Toast(l.T("stickers.export_failed") + ": " + mediaErrorText(result.err))
			} else if result.path != "" {
				d.modal.Toast(l.Format("stickers.saved", map[string]string{"path": result.path}))
			}
			return
		}
		if result.err != nil {
			key := "stickers.failed"
			if d.pack != nil {
				key = "stickers.action_failed"
			} else {
				d.err = result.err
			}
			d.modal.Toast(l.T(key) + ": " + mediaErrorText(result.err))
		}
		if result.err == nil {
			if result.pack != nil {
				d.setPack(result.pack)
			}
			if result.installed != nil && d.pack != nil {
				d.pack.Installed = *result.installed
			}
		}
	default:
	}
}

// stopRefresh ends the fetch of a set shown from the cache, for an action
// that takes its place; the set shown stays.
func (d *stickerSetDialog) stopRefresh() {
	if d.refreshing {
		if d.cancel != nil {
			d.cancel()
		}
		d.refreshing = false
	}
}

// setPack shows pack, keeping the cells when it has as many items, so that
// a set brought up to date does not lose the pointer's hover.
func (d *stickerSetDialog) setPack(pack *model.StickerSet) {
	d.pack = pack
	if len(d.cells) != len(pack.Items) {
		d.cells = make([]surface, len(pack.Items))
	}
}

func (d *stickerSetDialog) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	d.update(l)
	if result := d.authorResult; result != nil {
		d.authorResult = nil
		if result.author != nil && result.err == nil && p.openChat != nil {
			d.modal.Close()
			p.openChat(*result.author, 0)
		} else {
			text := strconv.FormatInt(result.authorID, 10)
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
			d.modal.Toast(l.Format("stickers.author_copied", map[string]string{"id": text}))
		}
	}
	d.modal.back = d.back
	if !d.modal.closing {
		if d.close.Clicked(gtx) {
			d.menuOpen = false
			d.modal.Close()
		}
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &d.menuDismiss, Kinds: pointer.Press})
			if !ok {
				break
			}
			// Gio reports this target's pointer position from the menu's
			// origin, even though its dismiss area covers the whole card.
			if e, ok := ev.(pointer.Event); ok && !image.Pt(int(e.Position.X), int(e.Position.Y)).In(image.Rectangle{Max: d.menuRect.Size()}) {
				d.menuOpen = false
			}
		}
		if !d.busy && d.pack != nil && d.more.Clicked(gtx) {
			d.menuOpen = !d.menuOpen
		}
		if d.author.Clicked(gtx) && !d.busy && d.menuOpen && d.pack != nil && d.pack.AuthorID != 0 {
			d.menuOpen = false
			d.findAuthor(p)
		}
		chosen := d.download.Clicked(gtx)
		if !d.busy && d.menuOpen && chosen {
			d.menuOpen = false
			d.export(p)
		}
	}
	if !d.busy && !d.modal.closing {
		if d.pack != nil && d.action.Clicked(gtx) {
			d.change(p, !d.pack.Installed)
		}
		if d.pack == nil && d.retry.Clicked(gtx) {
			d.fetch(p)
		}
	}
	shown := d.modal.Layout(gtx, d.busy, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(420))
		height := gtx.Dp(300)
		if d.pack != nil {
			cell := d.cell(gtx)
			columns := max(1, max(1, width-gtx.Dp(40))/max(1, cell.X))
			rows := (len(d.pack.Items) + columns - 1) / columns
			// As many rows as Telegram Desktop's 320 px list shows.
			height = gtx.Dp(178) + min(rows, max(1, gtx.Dp(320)/cell.Y))*cell.Y
		}
		size := image.Pt(width, min(gtx.Constraints.Max.Y, min(height, gtx.Dp(520))))
		gtx.Constraints = layout.Exact(size)
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			title := l.T("stickers.title")
			if d.pack != nil {
				title = d.pack.Title
			}
			headerHeight := 0
			dims := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					header := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return label(gtx, title, token.TypestyleTitleLarge, sc.Surface.OnColor, 2)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if d.pack == nil {
								return layout.Dimensions{}
							}
							return navigationButton(gtx, &d.more, iconMore, l.T("stickers.options"))
						}),
					)
					headerHeight = header.Size.Y
					return header
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if d.pack == nil {
						return layout.Dimensions{}
					}
					key := "stickers.count"
					if d.pack.Emoji {
						key = "stickers.emoji_count"
					}
					return label(gtx, l.Count(key, d.pack.Count, nil), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
				}),
				vspace(12),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if d.busy {
						return layout.Dimensions{Size: gtx.Constraints.Max}
					}
					if d.pack != nil {
						return d.grid(gtx, p)
					}
					return layout.Dimensions{}
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &d.close, l.T("stickers.close"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if d.busy {
								return layout.Dimensions{}
							}
							if d.pack == nil {
								return textButton(gtx, &d.retry, l.T("stickers.retry"))
							}
							text := l.T("stickers.add")
							if d.pack.Installed {
								text = l.T("stickers.remove")
							}
							return tonalButton(gtx, &d.action, text, 0)
						}),
					)
				}),
			)
			if d.busy {
				center := gtx
				center.Constraints.Min = center.Constraints.Max
				layout.Center.Layout(center, func(gtx layout.Context) layout.Dimensions { return d.loader.sized(gtx, l, 32) })
			}
			d.menuLayout(gtx, l, headerHeight)
			return dims
		}, defaultCardPadding)
	})
	if !shown {
		d.remember(p)
		p.dropStickerLoops()
		d.stop()
	}
}

// remember adds the media of the set shown to the chat's dialogStickers.
func (d *stickerSetDialog) remember(p *chatPage) {
	if d.pack == nil {
		return
	}
	if p.dialogStickers == nil {
		p.dialogStickers = map[string]bool{}
	}
	for _, item := range d.pack.Items {
		if item.Media.Media != nil {
			p.dialogStickers[item.Media.Media.ID] = true
		}
	}
}

func (d *stickerSetDialog) menuLayout(gtx layout.Context, l localization.Catalog, headerHeight int) {
	if d.pack == nil {
		return
	}
	size := gtx.Constraints.Max
	if d.menuOpen {
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &d.menuDismiss)
		area.Pop()
	} else {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	w := min(size.X, gtx.Dp(menuWidth))
	rows := 1
	if d.pack.AuthorID != 0 {
		rows++
	}
	h := 2*gtx.Dp(menuPadding) + rows*gtx.Dp(menuItemHeight)
	y := min(headerHeight, max(0, size.Y-h))
	rect := image.Rect(size.X-w, y, size.X, y+h)
	d.menuRect = rect
	radius := gtx.Dp(12)
	d.menu.Layout(gtx, d.menuOpen, rect, menuFromTopRight, radius, func(gtx layout.Context) layout.Dimensions {
		menuSize := gtx.Constraints.Max
		sc := scheme(gtx)
		defer clip.UniformRRect(image.Rectangle{Max: menuSize}, radius).Push(gtx.Ops).Pop()
		fillRounded(gtx, sc.SurfaceContainerHigh, menuSize, radius)
		event.Op(gtx.Ops, &d.menuPanel)
		row := func(index int, button *surface, text string, icon func(layout.Context, token.MatColor) layout.Dimensions) {
			y := gtx.Dp(menuPadding) + index*gtx.Dp(menuItemHeight)
			inRect(gtx, image.Rect(0, y, menuSize.X, y+gtx.Dp(menuItemHeight)), func(gtx layout.Context) layout.Dimensions {
				style := surfaceStyle{background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: text}
				return button.Layout(gtx, gtx.Constraints.Max, style, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, sc.SurfaceVariant.OnColor) }),
							layout.Rigid(layout.Spacer{Width: 12}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min = image.Point{}
									return label(gtx, text, token.TypestyleBodyMedium, sc.Surface.OnColor, 1)
								})
							}),
						)
					})
				})
			})
		}
		row(0, &d.download, l.T("stickers.download_zip"), iconDownload)
		if d.pack.AuthorID != 0 {
			row(1, &d.author, l.T("stickers.author"), iconPersonal)
		}
		return layout.Dimensions{Size: menuSize}
	})
}

// cell is the size of an item in the grid: emoji are smaller than
// stickers, as in Telegram Desktop's box (emojiSetSize, stickersSize).
func (d *stickerSetDialog) cell(gtx layout.Context) image.Point {
	if d.pack != nil && d.pack.Emoji {
		return image.Pt(gtx.Dp(40), gtx.Dp(40))
	}
	return image.Pt(gtx.Dp(64), gtx.Dp(64))
}

// canChoose reports whether an item may be sent to, or inserted into the
// draft of, the dialog's chat.
func (d *stickerSetDialog) canChoose(p *chatPage) bool {
	if p.composer == nil || p.frozen.Frozen() {
		return false
	}
	kind := model.SendSticker
	if d.pack != nil && d.pack.Emoji {
		kind = model.SendText
	}
	return p.composer.permissions(d.chat).Allows(kind)
}

func (d *stickerSetDialog) grid(gtx layout.Context, p *chatPage) layout.Dimensions {
	items := d.pack.Items
	if len(d.cells) != len(items) {
		d.cells = make([]surface, len(items))
	}
	tab := model.PickerStickers
	if d.pack.Emoji {
		tab = model.PickerEmoji
	}
	choose := d.canChoose(p)
	if !d.modal.closing {
		// A sticker is sent and an emoji goes into the draft, as the panel
		// does, whether the set is added or not.
		for i := range d.cells {
			if d.cells[i].Clicked(gtx) && choose && p.composer.chooseIn(gtx, tab, items[i]) {
				d.modal.Close()
				break
			}
		}
	}
	d.hover.Update(gtx, &d.list.List)
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	d.hover.Op(gtx)
	width := gtx.Constraints.Max.X
	cell := d.cell(gtx)
	columns := max(1, width/cell.X)
	rows := (len(items) + columns - 1) / columns
	d.list.Axis = layout.Vertical
	return d.list.Layout(gtx, rows, func(gtx layout.Context, row int) layout.Dimensions {
		width := gtx.Constraints.Max.X
		cellWidth := width / columns
		for col := 0; col < columns && row*columns+col < len(items); col++ {
			index := row*columns + col
			item := items[index]
			left := col * cellWidth
			if col == columns-1 {
				cellWidth = width - left
			}
			inRect(gtx, image.Rect(left, 0, left+cellWidth, cell.Y), func(gtx layout.Context) layout.Dimensions {
				draw := func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						hovered := d.cells[index].click.Hovered()
						cheap := hovered && p.media.CheapToPlay(item.Media, gtx.Constraints.Max, false)
						return d.item(gtx, p, item, p.animate || d.hover.Play(gtx, index, hovered, cheap))
					})
				}
				if index >= len(d.cells) {
					return draw(gtx)
				}
				sc := scheme(gtx)
				style := surfaceStyle{radius: gtx.Dp(8), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: item.Emoji}
				return d.cells[index].Layout(gtx, gtx.Constraints.Max, style, draw)
			})
		}
		return layout.Dimensions{Size: image.Pt(width, cell.Y)}
	})
}

func (d *stickerSetDialog) item(gtx layout.Context, p *chatPage, item model.PickerItem, animate bool) layout.Dimensions {
	status := p.media.StatusFit(item.Media, animate, gtx.Constraints.Max, false)
	im := status.Frame
	if im == nil {
		im = status.Preview
	}
	if im != nil {
		return widget.Image{Src: p.images.Op(im), Fit: widget.Contain}.Layout(gtx)
	}
	style := token.TypestyleTitleLarge
	if d.pack.Emoji {
		style = token.TypestyleTitleMedium
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return label(gtx, item.Emoji, style, scheme(gtx).Surface.OnColor, 1)
	})
}

func (d *stickerSetDialog) findAuthor(p *chatPage) {
	id := d.pack.AuthorID
	source, ok := p.source.(model.StickerSetAuthorSource)
	if !ok || p.openChat == nil {
		d.authorResult = &stickerSetResult{authorID: id}
		p.invalidate()
		return
	}
	d.stopRefresh()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	d.cancel = cancel
	d.busy = true
	d.results = make(chan stickerSetResult, 1)
	results := d.results
	go func() {
		chat, err := source.StickerSetAuthor(ctx, id)
		results <- stickerSetResult{authorID: id, author: &chat, err: err}
		p.invalidate()
	}()
}
