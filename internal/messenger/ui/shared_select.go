// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"slices"
	"sort"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// iconGoToMessage is the item of a shared file's menu that goes to its
// message.
var iconGoToMessage = wdk.RequireIconWidget(icons.ImageRemoveRedEye)

// sharedAction is an item of the menu of a shared file, as Telegram
// Desktop's shared media menu has them (info_media_list_widget.cpp,
// showContextMenu): for one alone, or for the selection it is in.
type sharedAction int

const (
	sharedGoTo sharedAction = iota
	sharedSave
	sharedForward
	sharedDelete
	sharedSelect
	sharedForwardSelected
	sharedDeleteSelected
	sharedClearSelection
	sharedActions
)

// sharedMenu is the context menu a right click opens on a shared file.
type sharedMenu struct {
	menu  contextMenu
	open  bool
	id    model.MessageID
	at    image.Point
	shown []sharedAction
	items [sharedActions]surface
	// press takes the right clicks over the dialog, for where the menu
	// opens; dismiss, the clicks around the open menu; panel, those on it.
	press, dismiss, panel byte
	pressAt               image.Point
}

// sharedCell takes a shared file's right clicks, which open its menu;
// while some are selected, it keeps the file's own clicks from it, as a
// press selects or unselects it then (sharedDrag).
type sharedCell struct{ tag byte }

// selectable tells whether the section shown lists messages of the chat,
// which may be selected: not a user's gifts, stories or groups.
func (p *chatInfo) selectable() bool {
	return !p.themePage && slices.Contains(model.SharedKinds, p.section)
}

// selecting tells whether some of the section's files are selected.
func (p *chatInfo) selecting() bool {
	return p.selectable() && len(p.renderer.selection.selected) > 0
}

// syncSelection gives the renderer the section's messages, oldest first,
// for the selection's actions, and forgets those selected it lists no
// more.
func (p *chatInfo) syncSelection() {
	msgs := append([]model.Message(nil), p.messages...)
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Key.MessageID < msgs[j].Key.MessageID })
	p.renderer.messages = msgs
	alive := make(map[model.MessageID]bool, len(msgs))
	for _, m := range msgs {
		alive[m.Key.MessageID] = true
	}
	p.renderer.pruneSelection(alive)
}

// clearShared drops the selection and the menu, as another section or
// chat shows.
func (p *chatInfo) clearShared() {
	p.renderer.clearSelection()
	p.renderer.messages = nil
	p.sharedMenu.open = false
	p.cells = map[model.MessageKey]*sharedCell{}
	p.drag = sharedDrag{}
	p.mode.open = false
	p.places = p.places[:0]
}

// dropDeleted takes the messages deleted from the section.
func (p *chatInfo) dropDeleted(ids []model.MessageID) {
	gone := make(map[model.MessageID]bool, len(ids))
	for _, id := range ids {
		gone[id] = true
	}
	kept := p.messages[:0]
	for _, m := range p.messages {
		if !gone[m.Key.MessageID] {
			kept = append(kept, m)
		}
	}
	if n := len(p.messages) - len(kept); n > 0 && p.counts != nil {
		p.counts[p.section] = max(0, p.counts[p.section]-n)
	}
	clear(p.messages[len(kept):])
	p.messages = kept
	p.syncSelection()
}

// toggleSelected selects m, or unselects it.
func (p *chatInfo) toggleSelected(m model.Message) {
	s := &p.renderer.selection
	if s.selected[m.Key.MessageID] {
		delete(s.selected, m.Key.MessageID)
		return
	}
	if s.selected == nil {
		s.selected = map[model.MessageID]bool{}
	}
	s.selected[m.Key.MessageID] = true
}

// cellInput takes the right clicks on m, drawn in size at the origin;
// call it after drawing m, for its area to be over it. While some are
// selected it takes the clicks the file would.
func (p *chatInfo) cellInput(gtx layout.Context, m model.Message, size image.Point) {
	if !p.selectable() {
		return
	}
	c := p.cells[m.Key]
	if c == nil {
		c = &sharedCell{}
		p.cells[m.Key] = c
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.tag, Kinds: pointer.Press})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		if e.Source == pointer.Mouse && e.Buttons == pointer.ButtonSecondary {
			p.openSharedMenu(gtx, m)
		}
	}
	if !p.selecting() {
		// The file takes its own clicks.
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	if p.selecting() {
		pointer.CursorPointer.Add(gtx.Ops)
	}
	event.Op(gtx.Ops, &c.tag)
}

// selectedMark draws over a tile of size whether it is selected, as
// Telegram Desktop's shared media: tinted, with a check in a circle at
// its lower right; while others are, an empty circle there.
func (p *chatInfo) selectedMark(gtx layout.Context, m model.Message, size image.Point) {
	if !p.selecting() {
		return
	}
	sc := scheme(gtx)
	selected := p.renderer.selection.selected[m.Key.MessageID]
	if selected {
		paint.FillShape(gtx.Ops, sc.Primary.Color.SetOpacity(.25).AsNRGBA(), clip.Rect{Max: size}.Op())
	}
	d := gtx.Dp(24)
	ring := max(1, gtx.Dp(2))
	at := image.Pt(size.X-d-gtx.Dp(6), size.Y-d-gtx.Dp(6))
	offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
		white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		paint.FillShape(gtx.Ops, white, clip.Ellipse{Max: image.Pt(d, d)}.Op(gtx.Ops))
		inner := clip.Ellipse{Min: image.Pt(ring, ring), Max: image.Pt(d-ring, d-ring)}.Op(gtx.Ops)
		if !selected {
			paint.FillShape(gtx.Ops, color.NRGBA{A: 90}, inner)
			return layout.Dimensions{}
		}
		paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), inner)
		icon := d - 2*gtx.Dp(5)
		offset(gtx, image.Pt((d-icon)/2, (d-icon)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(icon, icon), func(gtx layout.Context) layout.Dimensions {
				return iconSentMark(gtx, sc.Primary.OnColor)
			})
		})
		return layout.Dimensions{}
	})
}

// sharedMenuActions are the items of m's menu: go to it, save it, forward
// and delete it or the selection it is in, and select it or clear that.
func (p *chatInfo) sharedMenuActions(m model.Message) []sharedAction {
	r := p.renderer
	out := []sharedAction{}
	if p.openMessage != nil {
		out = append(out, sharedGoTo)
	}
	if r.selection.selected[m.Key.MessageID] {
		rights := r.selectionRights()
		if rights.Forward {
			out = append(out, sharedForwardSelected)
		}
		if rights.Delete {
			out = append(out, sharedDeleteSelected)
		}
		return append(out, sharedClearSelection)
	}
	if r.canSave(m) {
		out = append(out, sharedSave)
	}
	rights := r.rightsFor(messageParts(m))
	if rights.Forward {
		out = append(out, sharedForward)
	}
	if rights.Delete {
		out = append(out, sharedDelete)
	}
	return append(out, sharedSelect)
}

// openSharedMenu opens m's menu where the right click came.
func (p *chatInfo) openSharedMenu(gtx layout.Context, m model.Message) {
	shown := p.sharedMenuActions(m)
	if len(shown) == 0 {
		return
	}
	p.sharedMenu.open, p.sharedMenu.id, p.sharedMenu.at, p.sharedMenu.shown = true, m.Key.MessageID, p.sharedMenu.pressAt, shown
	p.sharedMenu.menu = contextMenu{}
	gtx.Execute(op.InvalidateCmd{})
}

// sharedMenuMessage is the message whose menu is open.
func (p *chatInfo) sharedMenuMessage() (model.Message, bool) {
	for _, m := range p.messages {
		if m.Key.MessageID == p.sharedMenu.id {
			return m, true
		}
	}
	return model.Message{}, false
}

// sharedMenuUpdate does what was chosen in the menu, and closes it on a
// click around it; call it before the dialog is laid out. press is the
// window's area, which tells where right clicks come.
func (p *chatInfo) sharedMenuUpdate(gtx layout.Context, l localization.Catalog) {
	m := &p.sharedMenu
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.press, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			m.pressAt = e.Position.Round()
		}
	}
	if !m.open {
		return
	}
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press}); !ok {
			break
		}
		m.open = false
	}
	msg, ok := p.sharedMenuMessage()
	if !ok || !p.selectable() {
		m.open = false
		return
	}
	for _, a := range m.shown {
		if m.open && m.items[a].Clicked(gtx) {
			m.open = false
			p.sharedDo(gtx, a, msg, l)
			gtx.Execute(op.InvalidateCmd{})
		}
	}
}

func (p *chatInfo) sharedDo(gtx layout.Context, a sharedAction, m model.Message, l localization.Catalog) {
	r := p.renderer
	parts := messageParts(m)
	switch a {
	case sharedGoTo:
		open := p.openMessage
		p.modal.Close()
		open(m)
	case sharedSave:
		if len(m.Attachments) > 1 {
			r.saveAlbum(m.Attachments, l)
		} else {
			r.saveMessage(m, r.saveWayOf(m), l)
		}
	case sharedForward:
		r.openForwardParts(gtx, parts, false)
	case sharedDelete:
		r.openDeleteParts(gtx, parts, r.rightsFor(parts))
	case sharedSelect:
		p.toggleSelected(m)
	case sharedForwardSelected:
		r.openForward(gtx)
	case sharedDeleteSelected:
		r.openDelete(gtx, r.selectionRights())
	case sharedClearSelection:
		r.clearSelection()
	}
}

func (a sharedAction) label(p *chatInfo, l localization.Catalog) string {
	switch a {
	case sharedGoTo:
		return l.T("menu.go_to_message")
	case sharedSave:
		if m, ok := p.sharedMenuMessage(); ok {
			if len(m.Attachments) > 1 {
				return l.T("menu.save_album")
			}
			return saveLabel(p.renderer.saveWayOf(m), l)
		}
		return l.T("menu.save_as")
	case sharedForward:
		return l.T("menu.forward")
	case sharedDelete:
		return l.T("menu.delete")
	case sharedSelect:
		return l.T("menu.select")
	case sharedForwardSelected:
		return l.T("menu.forward_selected")
	case sharedDeleteSelected:
		return l.T("menu.delete_selected")
	case sharedClearSelection:
		return l.T("menu.clear_selection")
	}
	return ""
}

func (a sharedAction) icon() wdk.IconWidget {
	switch a {
	case sharedGoTo:
		return iconGoToMessage
	case sharedSave:
		return iconDownload
	case sharedForward, sharedForwardSelected:
		return iconForward
	case sharedDelete, sharedDeleteSelected:
		return iconDelete
	}
	return iconSelect
}

// sharedMenuLayout draws the menu over the window while it is open or
// closing, and takes the right clicks for where it opens.
func (p *chatInfo) sharedMenuLayout(gtx layout.Context, l localization.Catalog) {
	m := &p.sharedMenu
	size := gtx.Constraints.Max
	func() {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
		defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
		event.Op(gtx.Ops, &m.press)
	}()
	items := make([]infoMenuItem, 0, len(m.shown))
	for _, a := range m.shown {
		items = append(items, infoMenuItem{item: &m.items[a], icon: a.icon(), text: a.label(p, l)})
	}
	p.layoutInfoMenu(gtx, &m.menu, m.open, m.at, &m.dismiss, &m.panel, items, nil, 0)
	p.layoutModeMenu(gtx, l)
}

// infoMenuItem is an item of a menu over the info: chosen draws it in the
// primary color, as the choice of a menu of choices.
type infoMenuItem struct {
	item   *surface
	icon   wdk.IconWidget
	text   string
	chosen bool
}

// layoutInfoMenu draws a menu of items opening from at over the window,
// while it is open or closing, and footer, footerHeight high, under them:
// dismiss takes the clicks around it while it is open, panel those on it.
func (p *chatInfo) layoutInfoMenu(gtx layout.Context, menu *contextMenu, open bool, at image.Point, dismiss, panel *byte, items []infoMenuItem, footer layout.Widget, footerHeight int) {
	size := gtx.Constraints.Max
	if open {
		// Clicks around the menu close it, and take nothing else.
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, dismiss)
		area.Pop()
	} else {
		// A closing menu lets clicks through to what is under it.
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	margin := gtx.Dp(8)
	// As wide as its longest text needs, the row's icon and paddings
	// beside it (menuRowChosen), and no narrower than the other menus.
	w := gtx.Dp(menuWidth)
	for _, it := range items {
		measure := gtx
		measure.Ops = new(op.Ops)
		measure.Constraints = layout.Constraints{Max: image.Pt(size.X, gtx.Dp(menuItemHeight))}
		text := label(measure, it.text, token.TypestyleBodyMedium, scheme(gtx).Surface.OnColor, 1)
		w = max(w, text.Size.X+gtx.Dp(16+24+12+16))
	}
	w = min(w, max(0, size.X-2*margin))
	h := 2*gtx.Dp(menuPadding) + len(items)*gtx.Dp(menuItemHeight)
	if footer != nil {
		h += footerHeight
	}
	rect, corner := menu.Place(gtx, at, size, image.Pt(w, h))
	radius := gtx.Dp(12)
	menu.Layout(gtx, open, rect, corner, radius, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		menuSize := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: menuSize}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, p.renderer.menuBackdrop(), menuSize, menu.bounds.Min, sc.SurfaceContainerHighest, radius)
		event.Op(gtx.Ops, panel)
		y := gtx.Dp(menuPadding)
		for _, it := range items {
			inRect(gtx, image.Rect(0, y, menuSize.X, y+gtx.Dp(menuItemHeight)), func(gtx layout.Context) layout.Dimensions {
				return menuRowChosen(gtx, it.item, it.icon, it.text, false, it.chosen)
			})
			y += gtx.Dp(menuItemHeight)
		}
		if footer != nil {
			inRect(gtx, image.Rect(0, y, menuSize.X, y+footerHeight), footer)
		}
		return layout.Dimensions{Size: menuSize}
	})
}
