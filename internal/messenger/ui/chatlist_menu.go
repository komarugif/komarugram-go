// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"strconv"
	"strings"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

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

// A right click on a chat of the list opens its menu, as Telegram Desktop's
// (window_peer_menu.cpp): pin it to the top of the list or unpin it, and
// mark it read when it has unread messages. The chats a store pins are
// limited (dialogs_pinned_limit, which Premium raises): a pin over the limit
// is not asked of Telegram, the list tells the limit instead.

// chatRowAction is an item of the menu of a chat of the list.
type chatRowAction int

const (
	chatRowPin chatRowAction = iota
	chatRowUnpin
	chatRowRead
	chatRowActions
)

// iconPinChat is the thumbtack of the pin items.
var iconPinChat wdk.IconWidget = func(gtx layout.Context, color token.MatColor) layout.Dimensions {
	return drawPin(gtx, min(gtx.Constraints.Max.X, gtx.Constraints.Max.Y), color)
}

// chatRowMenu is the menu of a chat of the list, and what it asks of the
// store.
type chatRowMenu struct {
	// store does what the menu offers; nil leaves the menu off.
	store model.ChatListActions
	// premium tells the pin limit; nil skips the check.
	premium model.PremiumSource
	// invalidate redraws the window when a pin is answered.
	invalidate func()

	menu  contextMenu
	open  bool
	id    int64
	at    image.Point
	press f32.Point
	rect  image.Rectangle
	rows  map[int64]*byte
	items [chatRowActions]surface
	area  byte
	// dismiss takes the presses around the open menu, and focus the keyboard
	// for its Escape on the frame after it opens.
	dismiss byte
	focus   bool
	// shown are the actions of the open menu.
	shown []chatRowAction

	mu sync.Mutex
	// failed is what the last pin came to, for the list's toast, until it
	// is told.
	failed error
}

// row is the tag that takes the right clicks on the row of chat.
func (m *chatRowMenu) row(chat int64) *byte {
	if m.rows == nil {
		m.rows = map[int64]*byte{}
	}
	t := m.rows[chat]
	if t == nil {
		t = new(byte)
		m.rows[chat] = t
	}
	return t
}

// actions are the items the menu of c offers; pins are only of the list of
// all chats, where a pin is the list's own.
func (m *chatRowMenu) actions(c model.Chat, sec section) []chatRowAction {
	if m.store == nil {
		return nil
	}
	var out []chatRowAction
	if sec.kind == sectionAll {
		if c.Pinned {
			out = append(out, chatRowUnpin)
		} else {
			out = append(out, chatRowPin)
		}
	}
	if c.Unread > 0 {
		out = append(out, chatRowRead)
	}
	return out
}

func chatRowLabel(a chatRowAction, l localization.Catalog) string {
	switch a {
	case chatRowPin:
		return l.T("chat_row.pin")
	case chatRowUnpin:
		return l.T("chat_row.unpin")
	}
	return l.T("chat_row.read")
}

func chatRowIcon(a chatRowAction) wdk.IconWidget {
	if a == chatRowRead {
		return iconRead
	}
	return iconPinChat
}

// update opens the menu on a right click and does what its items are
// clicked for. chats are the list, whose chat the menu is about.
func (m *chatRowMenu) update(gtx layout.Context, sec section, chats []model.Chat, shown []int64, toast *toast, l localization.Catalog) {
	if m.store == nil {
		return
	}
	m.tell(toast, l)
	// Where the right click was, in the list, and on which chat.
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.area, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			m.press = e.Position
		}
	}
	for _, id := range shown {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: m.row(id), Kinds: pointer.Press})
			if !ok {
				break
			}
			e, ok := ev.(pointer.Event)
			if !ok || e.Source != pointer.Mouse || e.Buttons != pointer.ButtonSecondary {
				continue
			}
			if c, ok := chatByID(chats, id); ok && len(m.actions(c, sec)) > 0 {
				m.open, m.id, m.focus = true, id, true
				m.menu = contextMenu{}
				m.at = image.Pt(int(m.press.X), int(m.press.Y))
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
	if !m.open {
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press}, key.FocusFilter{Target: &m.dismiss}, key.Filter{Focus: &m.dismiss, Name: key.NameEscape})
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			m.open = false
		case key.Event:
			if e.State == key.Press {
				m.open = false
			}
		}
	}
	c, ok := chatByID(chats, m.id)
	if !ok {
		m.open = false
		return
	}
	m.shown = m.actions(c, sec)
	for _, a := range m.shown {
		if !m.items[a].Clicked(gtx) {
			continue
		}
		m.open = false
		switch a {
		case chatRowPin, chatRowUnpin:
			m.pin(c, a == chatRowPin, chats, toast, l)
		case chatRowRead:
			m.store.MarkChatRead(c.ID)
		}
	}
}

func chatByID(chats []model.Chat, id int64) (model.Chat, bool) {
	for _, c := range chats {
		if c.ID == id {
			return c, true
		}
	}
	return model.Chat{}, false
}

// pin pins or unpins c: past the limit the toast tells so, and otherwise the
// store is asked in the background.
func (m *chatRowMenu) pin(c model.Chat, pin bool, chats []model.Chat, toast *toast, l localization.Catalog) {
	if pin && m.premium != nil {
		p := m.premium.Premium()
		if limit := p.Limit("dialogs_pinned_limit"); limit > 0 && model.PinnedCount(chats) >= limit {
			toast.Show(pinLimitText(p, limit, l))
			return
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := m.store.PinChat(ctx, c.ID, pin)
		m.mu.Lock()
		m.failed = err
		m.mu.Unlock()
		if m.invalidate != nil {
			m.invalidate()
		}
	}()
}

// pinLimitText tells the limit of pins, and what Premium would make it.
func pinLimitText(p model.Premium, limit int, l localization.Catalog) string {
	key := "chat_row.pin_limit"
	more := p
	more.Active = true
	if upper := more.Limit("dialogs_pinned_limit"); !p.Active && upper > limit {
		key = "chat_row.pin_limit_pro"
		return strings.NewReplacer("{limit}", strconv.Itoa(limit), "{premium}", strconv.Itoa(upper)).Replace(l.T(key))
	}
	return strings.NewReplacer("{limit}", strconv.Itoa(limit)).Replace(l.T(key))
}

// tell shows in the toast what the last pin came to, if it failed.
func (m *chatRowMenu) tell(toast *toast, l localization.Catalog) {
	m.mu.Lock()
	err := m.failed
	m.failed = nil
	m.mu.Unlock()
	switch {
	case err == nil:
	case errors.Is(err, model.ErrPinnedTooMuch):
		limit := 0
		var p model.Premium
		if m.premium != nil {
			p = m.premium.Premium()
			limit = p.Limit("dialogs_pinned_limit")
		}
		toast.Show(pinLimitText(p, limit, l))
	default:
		toast.Show(l.T("chat_row.pin_failed"))
	}
}

// areaOp makes the list take presses for their position; they go on to the
// rows under it. It is laid out over them.
func (m *chatRowMenu) areaOp(gtx layout.Context) {
	if m.store == nil {
		return
	}
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &m.area)
}

// rowOp makes the row of chat, of size, take right clicks.
func (m *chatRowMenu) rowOp(gtx layout.Context, chat int64, size image.Point) {
	if m.store == nil {
		return
	}
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, m.row(chat))
}

// layout draws the menu over the list of size, while it is open or closing.
func (m *chatRowMenu) layout(gtx layout.Context, bd *blurBackdrop, l localization.Catalog) {
	if m.store == nil {
		return
	}
	size := gtx.Constraints.Max
	if m.open {
		// Clicks around the menu close it, and take nothing else.
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &m.dismiss)
		area.Pop()
		if m.focus {
			gtx.Execute(key.FocusCmd{Tag: &m.dismiss})
			m.focus = false
		}
	} else {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	actions := m.shown
	if len(actions) == 0 {
		return
	}
	margin := gtx.Dp(8)
	w := min(gtx.Dp(menuWidth), max(0, size.X-2*margin))
	h := 2*gtx.Dp(menuPadding) + len(actions)*gtx.Dp(menuItemHeight)
	rect, corner := m.menu.Place(gtx, m.at, size, image.Pt(w, h))
	m.rect = rect
	radius := gtx.Dp(12)
	m.menu.Layout(gtx, m.open, m.rect, corner, radius, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		menuSize := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: menuSize}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, bd, menuSize, m.menu.bounds.Min, sc.SurfaceContainerHigh, radius)
		// The menu takes its own clicks from the dismissing area under it.
		event.Op(gtx.Ops, &m.menu)
		y := gtx.Dp(menuPadding)
		for _, a := range actions {
			height := gtx.Dp(menuItemHeight)
			text := chatRowLabel(a, l)
			inRect(gtx, image.Rect(0, y, menuSize.X, y+height), func(gtx layout.Context) layout.Dimensions {
				row := gtx.Constraints.Max
				style := surfaceStyle{background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: text}
				return m.items[a].Layout(gtx, row, style, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.Y = row.Y
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								px := gtx.Dp(20)
								return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
									return chatRowIcon(a)(gtx, sc.SurfaceVariant.OnColor)
								})
							}),
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
			y += height
		}
		return layout.Dimensions{Size: menuSize}
	})
}
