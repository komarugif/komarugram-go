// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"slices"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/wdk"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// The menu a right click opens on an item of the picker, as Telegram
// Desktop's panel has it (gifs_list_widget.cpp, stickers_list_widget.cpp,
// fillContextMenu): a GIF found is added to the saved ones, a saved one
// deleted from them, a recent sticker taken off the recent.

// pickerAction is an item of the menu of an item of the picker.
type pickerAction int

const (
	pickerSaveGIF pickerAction = iota
	pickerDeleteGIF
	pickerRemoveRecent
	pickerActions
)

var pickerActionIDs = [pickerActions]string{"save_gif", "delete_gif", "remove_recent"}

func (a pickerAction) id() string { return pickerActionIDs[a] }

func (a pickerAction) label(l localization.Catalog) string {
	return l.T([pickerActions]string{"composer.save_gif", "composer.delete_gif", "composer.recent_remove"}[a])
}

func (a pickerAction) icon() wdk.IconWidget {
	if a == pickerSaveGIF {
		return iconGIF
	}
	return iconDelete
}

// pickerItemMenu is the menu of an item of the picker right-clicked.
type pickerItemMenu struct {
	menu contextMenu
	open bool
	// item is the item it is of, in tab, at where it opened in the
	// picker; shown, its actions.
	item  model.PickerItem
	tab   model.PickerTab
	at    image.Point
	shown []pickerAction
	items [pickerActions]surface
	// dismiss takes the clicks around the open menu; panel, those on it.
	dismiss, panel byte
}

// pickerEdit is what came of an action of the menu of an item of the
// picker, done in the background.
type pickerEdit struct {
	tab model.PickerTab
	err error
}

// itemActions are the actions of the menu of item, in a row of the list
// that is recent when it is the recent stickers'.
func (c *messageComposer) itemActions(item model.PickerItem, recent bool) []pickerAction {
	var out []pickerAction
	switch c.tab {
	case model.PickerGIF:
		gifs, ok := c.source.(model.SavedGIFStore)
		if !ok || item.Media.Media == nil {
			break
		}
		switch {
		case c.query == "" || gifs.SavedGIF(item.ID):
			out = append(out, pickerDeleteGIF)
		case item.DocumentID != 0:
			// A GIF found on the web, not in Telegram, cannot be.
			out = append(out, pickerSaveGIF)
		}
	case model.PickerStickers:
		if _, ok := c.source.(model.RecentStickerStore); ok && recent {
			out = append(out, pickerRemoveRecent)
		}
	}
	return arrangeMenu(c.menus, menuPicker, out, pickerAction.id)
}

// openItemMenu opens the menu of the item of tile right-clicked at at, in
// the picker, whose list has rows.
func (c *messageComposer) openItemMenu(gtx layout.Context, tile holdTile, at image.Point, rows []pickerRow) {
	m := &c.itemMenu
	recent := false
	if n := tile.row; c.tab == model.PickerStickers && c.query == "" && n < len(rows) {
		row := rows[n]
		recent = row.pack == 0 && row.section == 0 && row.featured == nil &&
			slices.ContainsFunc(row.items, func(i model.PickerItem) bool { return i.ID == tile.item.ID })
	}
	shown := c.itemActions(tile.item, recent)
	if len(shown) == 0 {
		return
	}
	*m = pickerItemMenu{open: true, item: tile.item, tab: c.tab, at: at, shown: shown}
	gtx.Execute(op.InvalidateCmd{})
}

// itemMenuUpdate does what was chosen in the menu of an item, and closes
// it on a click around it; call it before the picker is laid out.
func (c *messageComposer) itemMenuUpdate(gtx layout.Context, l localization.Catalog) {
	m := &c.itemMenu
	if !m.open {
		return
	}
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press}); !ok {
			break
		}
		m.open = false
	}
	if !c.pickerOpen || m.tab != c.tab {
		m.open = false
	}
	for _, a := range m.shown {
		if m.open && m.items[a].Clicked(gtx) {
			m.open = false
			c.itemDo(a, m.item)
			gtx.Execute(op.InvalidateCmd{})
		}
	}
}

// itemDo does a of the menu of item: the list changes at once, and the
// store is told in the background; a failure is told, and the list asked
// for again.
func (c *messageComposer) itemDo(a pickerAction, item model.PickerItem) {
	tab := c.tab
	var do func(context.Context) error
	switch a {
	case pickerSaveGIF, pickerDeleteGIF:
		gifs, ok := c.source.(model.SavedGIFStore)
		if !ok {
			return
		}
		unsave := a == pickerDeleteGIF
		if unsave && c.query == "" {
			c.page.Items = model.WithoutItem(c.page.Items, item.ID)
		}
		do = func(ctx context.Context) error { return gifs.SaveGIF(ctx, item.Media, unsave) }
	case pickerRemoveRecent:
		recent, ok := c.source.(model.RecentStickerStore)
		if !ok {
			return
		}
		c.page.Recent = model.WithoutItem(c.page.Recent, item.ID)
		c.recent[tab] = model.WithoutItem(c.recent[tab], item.ID)
		do = func(ctx context.Context) error { return recent.RemoveRecentSticker(ctx, item) }
	default:
		return
	}
	go func() {
		edit := pickerEdit{tab: tab}
		defer func() {
			select {
			case c.pickerEdits <- edit:
				c.invalidate()
			case <-c.ctx.Done():
			}
		}()
		defer crash.Recover("picker menu", func(e *crash.Panic) { edit.err = e })
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		defer cancel()
		edit.err = do(ctx)
	}()
}

// itemEdited takes what came of an action of the menu of an item.
func (c *messageComposer) itemEdited(edit pickerEdit, l localization.Catalog) {
	if edit.err == nil {
		return
	}
	if errors.Is(edit.err, model.ErrSavedGIFLimit) {
		if text := savedGIFLimitText(c.source, l); text != "" {
			c.pickerToast.Show(text)
		}
		return
	}
	c.pickerToast.Show(mediaErrorText(edit.err))
	if c.pickerOpen && c.tab == edit.tab && c.source != nil {
		// What the list shows went back as it was.
		c.request(l, false)
	}
}

// savedGIFLimitText tells that a GIF saved took the place of the oldest,
// and how many Premium keeps, as Telegram Desktop's toast does; "" with
// Premium, when it tells nothing.
func savedGIFLimitText(source any, l localization.Catalog) string {
	premium := model.Premium{}
	if p, ok := source.(model.PremiumSource); ok {
		premium = p.Premium()
	}
	if premium.Active {
		return ""
	}
	raised := premium
	raised.Active = true
	more := map[string]string{"link": l.T("composer.gif_limit_link")}
	return l.Count("composer.gif_limit", model.SavedGIFLimit(premium), nil) + "\n" + l.Count("composer.gif_limit_more", model.SavedGIFLimit(raised), more)
}

// itemMenuLayout draws the menu of an item over the picker, of the size of
// the constraints, which is at origin in the page, while it is open or
// closing.
func (c *messageComposer) itemMenuLayout(gtx layout.Context, l localization.Catalog, p *chatPage, origin image.Point) {
	m := &c.itemMenu
	size := gtx.Constraints.Max
	if m.open {
		// Clicks around the menu close it, and take nothing else.
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &m.dismiss)
		area.Pop()
	} else {
		// A closing menu lets clicks through to the items under it.
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	margin := gtx.Dp(8)
	w := min(gtx.Dp(menuWidth), max(0, size.X-2*margin))
	h := 2*gtx.Dp(menuPadding) + len(m.shown)*gtx.Dp(menuItemHeight)
	rect, corner := m.menu.Place(gtx, m.at, size, image.Pt(w, h))
	radius := gtx.Dp(12)
	m.menu.Layout(gtx, m.open, rect, corner, radius, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		menuSize := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: menuSize}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, p.menuBackdrop(), menuSize, origin.Add(m.menu.bounds.Min), sc.SurfaceContainerHighest, radius)
		event.Op(gtx.Ops, &m.panel)
		y := gtx.Dp(menuPadding)
		for _, a := range m.shown {
			inRect(gtx, image.Rect(0, y, menuSize.X, y+gtx.Dp(menuItemHeight)), func(gtx layout.Context) layout.Dimensions {
				return menuRow(gtx, &m.items[a], a.icon(), a.label(l), false)
			})
			y += gtx.Dp(menuItemHeight)
		}
		return layout.Dimensions{Size: menuSize}
	})
}

// saveGIF adds the GIF of m to the saved ones, first, as Telegram
// Desktop's message menu does, and tells only when the oldest made room.
func (p *chatPage) saveGIF(m model.Message, l localization.Catalog) {
	gifs, ok := p.source.(model.SavedGIFStore)
	if !ok {
		return
	}
	go func() {
		defer crash.Recover("save GIF", func(e *crash.Panic) { p.reportMedia(e) })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		switch err := gifs.SaveGIF(ctx, m, false); {
		case errors.Is(err, model.ErrSavedGIFLimit):
			if text := savedGIFLimitText(p.source, l); text != "" {
				p.reportNotice(text)
			}
		case err != nil:
			p.reportMedia(err)
		}
	}()
}
