// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// The search history, as Telegram Desktop shows it: while nothing is typed,
// the chats picked from search results before, the last one first, under a
// "Recent" heading with a button that clears them after asking. A right
// click on one offers to remove it, or all of them.

// recentSearch is the search history part of the search panel.
type recentSearch struct {
	store model.RecentChats
	// shown are the chats the list shows this frame, none while something
	// is typed.
	shown []model.Chat
	clear surface
	// confirm asks before clearing; yes and no are its buttons.
	confirm modal
	yes, no surface
	// menu is the menu of a chat right-clicked: remove it, or all.
	menu    contextMenu
	open    bool
	id      int64
	at      image.Point
	remove  surface
	all     surface
	press   f32.Point
	rect    image.Rectangle
	rows    map[int64]*byte
	area    byte
	dismiss byte
	// focus takes the keyboard from the search field for the menu's
	// Escape, on the frame after it opens.
	focus bool
}

// recentRow is the tag that takes right clicks on the row of chat.
func (r *recentSearch) recentRow(chat int64) *byte {
	if r.rows == nil {
		r.rows = map[int64]*byte{}
	}
	t := r.rows[chat]
	if t == nil {
		t = new(byte)
		r.rows[chat] = t
	}
	return t
}

// refresh takes the history when the search shows it: nothing typed, and
// chats to look for.
func (r *recentSearch) refresh(text string, section model.SearchSection) {
	r.shown = nil
	if r.store == nil || strings.TrimSpace(text) != "" || section != model.SearchChats {
		return
	}
	r.shown = r.store.RecentChats()
}

// update handles the clear button, the confirmation and the menu of a
// chat.
func (r *recentSearch) update(gtx layout.Context) {
	if r.store == nil {
		return
	}
	if r.clear.Clicked(gtx) {
		r.confirm.Open()
	}
	if r.no.Clicked(gtx) {
		r.confirm.Close()
	}
	if r.yes.Clicked(gtx) && r.confirm.Shown() {
		r.store.ClearRecentChats()
		r.confirm.Close()
	}
	// Where the right click was, in the results, and on which chat.
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &r.area, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			r.press = e.Position
		}
	}
	for _, c := range r.shown {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: r.recentRow(c.ID), Kinds: pointer.Press})
			if !ok {
				break
			}
			if e, ok := ev.(pointer.Event); ok && e.Source == pointer.Mouse && e.Buttons == pointer.ButtonSecondary {
				r.open, r.id, r.focus = true, c.ID, true
				r.menu = contextMenu{}
				r.at = image.Pt(int(r.press.X), int(r.press.Y))
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
	if !r.open {
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &r.dismiss, Kinds: pointer.Press}, key.FocusFilter{Target: &r.dismiss}, key.Filter{Focus: &r.dismiss, Name: key.NameEscape})
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			r.open = false
		case key.Event:
			if e.State == key.Press {
				r.open = false
			}
		}
	}
	if r.remove.Clicked(gtx) {
		r.store.RemoveRecentChat(r.id)
		r.open = false
	}
	if r.all.Clicked(gtx) {
		r.open = false
		r.confirm.Open()
	}
}

// areaOp makes the results area take presses for their position; they go
// on to the rows under it. It is laid out over them.
func (r *recentSearch) areaOp(gtx layout.Context) {
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &r.area)
}

// rowOp makes the row of chat, of size, take right clicks.
func (r *recentSearch) rowOp(gtx layout.Context, chat int64, size image.Point) {
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, r.recentRow(chat))
}

// layoutHeading draws "Recent" and the button that clears it.
func (r *recentSearch) layoutHeading(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Inset{Top: 8, Bottom: 2, Left: chatAvatarInset, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("search.recent"), token.TypestyleLabelLarge, sc.Primary.Color, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return textButton(gtx, &r.clear, l.T("search.recent_clear"))
			}),
		)
	})
}

// layoutMenu draws the menu of the chat right-clicked, in the results area
// of size.
func (r *recentSearch) layoutMenu(gtx layout.Context, bd *blurBackdrop, l localization.Catalog) {
	size := gtx.Constraints.Max
	if r.open {
		// Clicks around the menu close it, and take nothing else.
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &r.dismiss)
		area.Pop()
		if r.focus {
			gtx.Execute(key.FocusCmd{Tag: &r.dismiss})
			r.focus = false
		}
	} else {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	margin := gtx.Dp(8)
	w := min(gtx.Dp(menuWidth), max(0, size.X-2*margin))
	h := 2*gtx.Dp(menuPadding) + 2*gtx.Dp(menuItemHeight)
	rect, corner := r.menu.Place(gtx, r.at, size, image.Pt(w, h))
	r.rect = rect
	radius := gtx.Dp(12)
	r.menu.Layout(gtx, r.open, rect, corner, radius, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		menuSize := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: menuSize}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, bd, menuSize, r.menu.bounds.Min, sc.SurfaceContainerHigh, radius)
		// The menu takes its own clicks from the dismissing area under it.
		event.Op(gtx.Ops, &r.menu)
		y := gtx.Dp(menuPadding)
		for _, item := range []struct {
			s    *surface
			text string
		}{{&r.remove, l.T("search.recent_remove")}, {&r.all, l.T("search.recent_clear_all")}} {
			inRect(gtx, image.Rect(0, y, menuSize.X, y+gtx.Dp(menuItemHeight)), func(gtx layout.Context) layout.Dimensions {
				row := gtx.Constraints.Max
				style := surfaceStyle{background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: item.text}
				return item.s.Layout(gtx, row, style, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.Y = row.Y
						return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min = image.Point{}
							return label(gtx, item.text, token.TypestyleBodyMedium, sc.Surface.OnColor, 1)
						})
					})
				})
			})
			y += gtx.Dp(menuItemHeight)
		}
		return layout.Dimensions{Size: menuSize}
	})
}

// layoutConfirm asks whether to clear the history, over the window.
func (r *recentSearch) layoutConfirm(gtx layout.Context, l localization.Catalog) {
	if !r.confirm.Shown() {
		return
	}
	sc := scheme(gtx)
	r.confirm.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(400))
		return r.confirm.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("search.recent_clear_sure"), token.TypestyleBodyLarge, sc.Surface.OnColor, 4)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &r.no, l.T("history.cancel")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &r.yes, l.T("search.recent_clear")) }),
					)
				}),
			)
		}, defaultCardPadding)
	})
}
