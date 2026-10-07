// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// chatMenuAction is an item of the menu of a chat's header.
type chatMenuAction int

// The items, in the order Telegram Desktop's menu of a chat has the ones
// that change nothing (window_peer_menu.cpp, Filler::fillHistoryActions),
// with AyuGram's "Jump to beginning".
const (
	chatMenuSearch chatMenuAction = iota
	chatMenuInfo
	// chatMenuTheme is Telegram Desktop's Change Colors: the chat's theme.
	chatMenuTheme
	chatMenuBeginning
	chatMenuFiltered
	chatMenuLeave
	chatMenuActions
)

// headButton is the size of a button of the header.
const headButton = 40

// chatMenu is the menu the header's button opens, and the header's
// buttons.
type chatMenu struct {
	menu  contextMenu
	open  bool
	rect  image.Rectangle
	items [chatMenuActions]surface
	// search and more are the header's buttons; dismiss takes clicks around
	// the open menu.
	search, more surface
	dismiss      byte
}

// chatMenuActions are the items the menu of the open chat offers.
func (p *chatPage) chatMenuActions() []chatMenuAction {
	var out []chatMenuAction
	if p.canSearchChat() {
		out = append(out, chatMenuSearch)
	}
	out = append(out, chatMenuInfo)
	if p.appearance != nil && p.appearance.source != nil && p.threadRoot == 0 {
		out = append(out, chatMenuTheme)
	}
	if _, ok := p.source.(model.MessageRevealer); ok && p.threadRoot == 0 {
		out = append(out, chatMenuBeginning)
	}
	if p.filtered > 0 {
		out = append(out, chatMenuFiltered)
	}
	if p.canLeaveChat() {
		out = append(out, chatMenuLeave)
	}
	return out
}

func (p *chatPage) chatMenuLabel(a chatMenuAction, l localization.Catalog) string {
	switch a {
	case chatMenuLeave:
		if p.kind == model.KindChannel {
			return l.T("membership.leave_channel")
		}
		return l.T("membership.leave_group")
	case chatMenuSearch:
		return l.T("chat_menu.search")
	case chatMenuInfo:
		switch p.kind {
		case model.KindGroup:
			return l.T("chat_menu.group")
		case model.KindChannel:
			return l.T("chat_menu.channel")
		}
		return l.T("chat_menu.profile")
	case chatMenuTheme:
		return l.T("chat_theme.title")
	case chatMenuBeginning:
		return l.T("chat_menu.beginning")
	case chatMenuFiltered:
		if p.showFiltered[p.chat] {
			return l.T("chat_menu.hide_filtered")
		}
		return l.T("chat_menu.show_filtered")
	}
	return ""
}

func chatMenuIcon(a chatMenuAction) wdk.IconWidget {
	switch a {
	case chatMenuLeave:
		return iconLogOut
	case chatMenuSearch:
		return iconSearch
	case chatMenuInfo:
		return iconInfo
	case chatMenuFiltered:
		return iconFilter
	case chatMenuTheme:
		return iconPalette
	}
	return iconToTop
}

// headActionsWidth is how much of the header's end its buttons take; a
// thread's header has search only.
func (p *chatPage) headActionsWidth(gtx layout.Context, thread bool) int {
	n := 1
	if thread {
		n = 0
	}
	if p.canSearchChat() {
		n++
	}
	return n * gtx.Dp(headButton)
}

// layoutHeadActions draws the header's buttons, search and the menu, at
// its end, right to x, and takes their clicks. A thread's header, a topic's
// or a post's comments', has search only: the menu is its chat's.
func (p *chatPage) layoutHeadActions(gtx layout.Context, header image.Point, x int, thread bool, l localization.Catalog) {
	m := &p.chatMenu
	if m.search.Clicked(gtx) {
		p.openChatSearch(gtx)
	}
	if m.more.Clicked(gtx) && !thread {
		m.open = !m.open
	}
	sc := scheme(gtx)
	button := gtx.Dp(headButton)
	draw := func(s *surface, icon wdk.IconWidget, name string) {
		x -= button
		offset(gtx, image.Pt(x, (header.Y-button)/2), func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(button, button)
			style := surfaceStyle{radius: button / 2, content: sc.Surface.OnColor, button: name}
			return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
				px := gtx.Dp(24)
				return offset(gtx, image.Pt((button-px)/2, (button-px)/2), func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, sc.SurfaceVariant.OnColor)
					})
				})
			})
		})
	}
	if !thread {
		draw(&m.more, iconMore, l.T("chat_menu.more"))
	}
	if p.canSearchChat() {
		draw(&m.search, iconSearch, l.T("chat_menu.search"))
	}
	if thread {
		return
	}
	// The menu opens under its button.
	actions := p.chatMenuActions()
	width := min(gtx.Dp(menuWidth), header.X-gtx.Dp(16))
	height := 2*gtx.Dp(menuPadding) + len(actions)*gtx.Dp(menuItemHeight)
	right := x + 2*button - gtx.Dp(4)
	if !p.canSearchChat() {
		right = x + button - gtx.Dp(4)
	}
	m.rect = image.Rect(right-width, header.Y-gtx.Dp(4), right, header.Y-gtx.Dp(4)+height)
}

// chatMenuUpdate does what was chosen in the header's menu.
func (p *chatPage) chatMenuUpdate(gtx layout.Context) {
	m := &p.chatMenu
	if !m.open {
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			m.open = false
		}
	}
	for m.open {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			m.open = false
		}
	}
	for _, a := range p.chatMenuActions() {
		if !m.items[a].Clicked(gtx) {
			continue
		}
		m.open = false
		switch a {
		case chatMenuLeave:
			p.askLeave()
		case chatMenuSearch:
			p.openChatSearch(gtx)
		case chatMenuInfo:
			p.infoAsked = true
		case chatMenuTheme:
			p.infoAsked, p.themeAsked = true, true
		case chatMenuBeginning:
			// The chat's first message, or the history around where it was.
			p.jumpTo(1)
		case chatMenuFiltered:
			if p.showFiltered == nil {
				p.showFiltered = map[int64]bool{}
			}
			p.showFiltered[p.chat] = !p.showFiltered[p.chat]
			// The history is read and filtered again.
			p.revision = 0
		}
		p.invalidate()
	}
}

// takeInfoAsked reports, once, whether the menu asked for the chat's info.
func (p *chatPage) takeInfoAsked() bool {
	asked := p.infoAsked
	p.infoAsked = false
	p.themeShown, p.themeAsked = p.themeAsked, false
	return asked
}

// layoutChatMenu draws the header's menu over the page while it is open or
// closing.
func (p *chatPage) layoutChatMenu(gtx layout.Context, l localization.Catalog) {
	m := &p.chatMenu
	p.chatMenuUpdate(gtx)
	if m.open {
		area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
		event.Op(gtx.Ops, &m.dismiss)
		area.Pop()
	} else {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	actions := p.chatMenuActions()
	radius := gtx.Dp(12)
	m.menu.Layout(gtx, m.open, m.rect, menuFromTopRight, radius, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		size := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		// The menu is drawn over the whole page, the history recorded is of
		// its body under the header.
		overlayFill(gtx, p.menuBackdrop().shifted(image.Pt(0, gtx.Dp(chatHeaderSize))), size, m.menu.bounds.Min, sc.SurfaceContainerHigh, radius)
		// Clicks on the menu itself do not close it.
		event.Op(gtx.Ops, &m.menu)
		y := gtx.Dp(menuPadding)
		for _, a := range actions {
			height := gtx.Dp(menuItemHeight)
			inRect(gtx, image.Rect(0, y, size.X, y+height), func(gtx layout.Context) layout.Dimensions {
				row := gtx.Constraints.Max
				text := p.chatMenuLabel(a, l)
				content, iconColor := sc.Surface.OnColor, sc.SurfaceVariant.OnColor
				if a == chatMenuLeave {
					content, iconColor = sc.Error.Color, sc.Error.Color
				}
				style := surfaceStyle{background: content.SetOpacity(0), content: content, button: text}
				return m.items[a].Layout(gtx, row, style, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return chatMenuIcon(a)(gtx, iconColor) }),
							layout.Rigid(layout.Spacer{Width: 12}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min = image.Point{}
									return label(gtx, text, token.TypestyleBodyMedium, content, 1)
								})
							}),
						)
					})
				})
			})
			y += height
		}
		return layout.Dimensions{Size: size}
	})
}
