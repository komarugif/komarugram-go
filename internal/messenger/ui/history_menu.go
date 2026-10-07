// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"io"
	"regexp"
	"strings"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// menuAction is an item of a message's context menu.
type menuAction int

// The actions, in the order Telegram Desktop's menu has them
// (history/view/history_view_context_menu.cpp, FillContextMenuItems).
const (
	actionReply menuAction = iota
	actionCopySelected
	actionCopyText
	actionCopyLink
	actionForward
	actionForwardSelected
	actionDelete
	actionDeleteSelected
	actionSelect
	actionClearSelection
	actionEmojiPacks
	actionReacted
	actionRead
	actionEdits
	actionFilter
	actionTranslate
	actionRepeat
	actionSaveHTML
	menuActions
)

// messageMenu is the context menu a right click opens on a message.
type messageMenu struct {
	menu contextMenu
	open bool
	// id is the message the menu is about, the first of an album; at is
	// where the menu opened, and top where the history starts, in the
	// chat page.
	id     model.MessageID
	at     image.Point
	top    int
	corner menuCorner
	rect   image.Rectangle
	shown  []menuAction
	items  [menuActions]surface
	packs  emojiPackLookup
	// reactions is the strip at the top, when the chat allows reactions;
	// reactedOf, the reactions of the message, which its item counts.
	reactions reactionStrip
	reactedOf []model.Reaction
	// target takes right clicks on the history; dismiss, the clicks
	// around the open menu; panel, those on it. Their sizes keep their
	// addresses, which tag input, apart.
	target, dismiss, panel byte
}

// Sizes of the menu, near Telegram Desktop's.
const (
	menuWidth      = 260
	menuItemHeight = 40
	menuPackHeight = 56
	menuPadding    = 6
)

// menuArea takes right clicks on the history's messages. It is laid out
// over them, in the history's coordinates, and passes every input on.
func (p *chatPage) menuArea(gtx layout.Context, top int) {
	m := &p.messageMenu
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.target, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Source == pointer.Mouse && e.Buttons == pointer.ButtonSecondary {
			p.openMenu(gtx, e.Position, top)
		}
	}
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.target)
	area.Pop()
	pass.Pop()
}

// openMenu opens the menu of the message at pos in the history, which
// starts at top in the chat page.
func (p *chatPage) openMenu(gtx layout.Context, pos f32.Point, top int) {
	m := &p.messageMenu
	m.open = false
	i := p.rowAt(pos.Y)
	if i < 0 || i >= len(p.messages) || p.messages[i].Kind == model.MessageService || p.messages[i].Streaming {
		return
	}
	msg := p.messages[i]
	m.open, m.id, m.top = true, msg.Key.MessageID, top
	m.menu = contextMenu{}
	m.reactions.expanded = false
	m.at = image.Pt(int(pos.X), int(pos.Y)+top)
	m.packs.start(p, msg)
	gtx.Execute(op.InvalidateCmd{})
}

func (p *chatPage) closeMenu() {
	p.messageMenu.open = false
	p.messageMenu.packs.stop()
	p.entityMenu.close()
}

// menuMessage is the message whose menu is open.
func (p *chatPage) menuMessage() (model.Message, bool) {
	for _, m := range p.messages {
		if m.Key.MessageID == p.messageMenu.id {
			return m, true
		}
	}
	return model.Message{}, false
}

// menuText is the text Copy Text copies: the message's, or an album's
// caption.
func menuText(m model.Message) string {
	var texts []string
	for _, part := range messageParts(m) {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// menuSelectedText is the text selected in m, when some is.
func (p *chatPage) menuSelectedText(m model.Message) string {
	if r := p.menuSelectedRow(m); r != nil {
		return r.selectedText()
	}
	return ""
}

// menuSelectedRow is the row of m, or of a part of it, text is selected
// in, nil for none.
func (p *chatPage) menuSelectedRow(m model.Message) *messageRow {
	r := p.rows[m.Key.MessageID]
	if r == nil || p.activeText == nil {
		return nil
	}
	if p.activeText == r {
		return r
	}
	for _, child := range r.album {
		if p.activeText == child {
			return child
		}
	}
	return nil
}

// canReply reports whether what is sent to the open chat may reply to m.
func (p *chatPage) canReply(m model.Message) bool {
	if p.composer == nil || p.composer.source == nil || p.frozen.Frozen() || m.Key.MessageID <= 0 || m.Deleted {
		return false
	}
	rights, ok := p.source.(model.RightsSource)
	return !ok || rights.CanSend(p.chat)
}

// menuLink is the link of m, when its chat has links.
func (p *chatPage) menuLink(m model.Message) (link string, public, ok bool) {
	if linker, is := p.source.(model.MessageLinker); is {
		return linker.MessageLink(p.chat, m.Key.MessageID)
	}
	return "", false, false
}

// menuActions are the actions the menu of m shows, as Telegram Desktop's
// shows them: those for the selection when m is selected.
func (p *chatPage) menuActions(m model.Message) []menuAction {
	var out []menuAction
	parts := messageParts(m)
	if p.canReply(m) {
		out = append(out, actionReply)
	}
	selected := p.selection.selected[m.Key.MessageID]
	protected := false
	for _, part := range parts {
		protected = protected || part.NoForwards
	}
	if p.menuSelectedText(m) != "" {
		out = append(out, actionCopySelected)
	} else if !protected && !selected && menuText(m) != "" {
		out = append(out, actionCopyText)
	}
	if _, _, ok := p.menuLink(m); ok {
		out = append(out, actionCopyLink)
	}
	if selected {
		rights := p.selectionRights()
		if rights.Forward {
			out = append(out, actionForwardSelected)
		}
		if rights.Delete {
			out = append(out, actionDeleteSelected)
		}
		out = append(out, actionClearSelection)
	} else {
		rights := p.rightsFor(parts)
		if rights.Forward {
			out = append(out, actionForward)
		}
		if rights.Delete {
			out = append(out, actionDelete)
		}
		out = append(out, actionSelect)
	}
	if !selected && p.canRepeat(m) {
		out = append(out, actionRepeat)
	}
	// Telegram Desktop offers it for the only message selected; it is
	// offered for a message alone too.
	if canSaveHTML(m) && (!selected || len(p.selection.selected) == 1) {
		out = append(out, actionSaveHTML)
	}
	if p.messageMenu.packs.id == m.Key.MessageID && len(p.messageMenu.packs.found.refs) > 0 {
		out = append(out, actionEmojiPacks)
	}
	if _, ok := p.source.(model.ReactionLister); ok && m.ReactionsListed {
		out = append(out, actionReacted)
	}
	if _, ok := p.source.(model.Translator); ok && !m.Deleted && (menuText(m) != "" || p.menuSelectedText(m) != "") {
		out = append(out, actionTranslate)
	}
	if p.addFilter != nil && p.menuSelectedText(m) != "" {
		out = append(out, actionFilter)
	}
	if _, ok := p.source.(model.KeepStore); ok && !m.EditedAt.IsZero() && !m.Outgoing {
		out = append(out, actionEdits)
	}
	// Without read receipts, a message is read when asked, as AyuGram's
	// Read Message does.
	if g, ok := p.source.(model.GhostStore); ok && !g.Ghost().SendRead && !m.Outgoing && p.kind != model.KindSaved && p.threadRoot == 0 && m.Key.MessageID > 0 {
		out = append(out, actionRead)
	}
	return out
}

// menuUpdate does what was chosen in the menu. It runs before the page is
// laid out, which the action may change.
func (p *chatPage) menuUpdate(gtx layout.Context, l localization.Catalog) {
	m := &p.messageMenu
	m.packs.update()
	if !m.open {
		return
	}
	msg, ok := p.menuMessage()
	if !ok {
		p.closeMenu()
		return
	}
	if r, ok := m.reactions.update(gtx); ok {
		p.closeMenu()
		if reactor, ok := p.source.(model.Reactor); ok {
			reactor.ToggleReaction(msg, r, p.reportMedia)
		}
		gtx.Execute(op.InvalidateCmd{})
		return
	}
	for _, a := range m.shown {
		if m.items[a].Clicked(gtx) {
			p.closeMenu()
			p.menuDo(gtx, a, msg, l)
			gtx.Execute(op.InvalidateCmd{})
			return
		}
	}
}

func (p *chatPage) menuDo(gtx layout.Context, a menuAction, m model.Message, l localization.Catalog) {
	parts := messageParts(m)
	copyText := func(text string) {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
	}
	switch a {
	case actionReply:
		p.composer.replyTo(gtx, p.chat, m)
	case actionCopySelected:
		if r := p.menuSelectedRow(m); r != nil {
			copySelection(gtx, r)
		}
	case actionCopyText:
		copyText(menuText(m))
	case actionCopyLink:
		if link, public, ok := p.menuLink(m); ok {
			copyText(link)
			if public {
				p.toast.Show(l.T("menu.link_copied"))
			} else {
				p.toast.Show(l.T("menu.private_link"))
			}
		}
	case actionForward:
		p.openForwardParts(gtx, parts, false)
	case actionForwardSelected:
		p.openForward(gtx)
	case actionDelete:
		p.openDeleteParts(gtx, parts, p.rightsFor(parts))
	case actionDeleteSelected:
		p.openDelete(gtx, p.selectionRights())
	case actionSelect:
		if p.selection.selected == nil {
			p.selection.selected = map[model.MessageID]bool{}
		}
		p.selection.selected[m.Key.MessageID] = true
		p.activeText = nil
	case actionClearSelection:
		p.clearSelection()
	case actionReacted:
		p.reacted.open(p, m)
	case actionEdits:
		p.edits.open(p, m)
	case actionTranslate:
		p.translation.open(p, m, p.menuSelectedText(m), string(l.Language()))
	case actionRepeat:
		p.repeat(m)
	case actionSaveHTML:
		p.saveHTML(m, l)
	case actionFilter:
		// A filter of the words selected, in every chat, as AyuGram's
		// quick filter.
		p.addFilter(preferences.FilterPattern{Text: regexp.QuoteMeta(strings.TrimSpace(p.menuSelectedText(m))), CaseInsensitive: true})
		p.toast.Show(l.T("filters.added"))
	case actionRead:
		if g, ok := p.source.(model.GhostStore); ok {
			last := m.Key.MessageID
			for _, part := range parts {
				last = max(last, part.Key.MessageID)
			}
			g.MarkRead(p.chat, last, true)
		}
	case actionEmojiPacks:
		refs := p.messageMenu.packs.found.refs
		if len(refs) == 1 {
			p.stickers.open(p, refs[0])
		} else {
			p.emojiPacks.open(p, refs)
		}
	}
}

// menuLabel is the text of a, an action of m's menu.
func (p *chatPage) menuLabel(a menuAction, l localization.Catalog) string {
	switch a {
	case actionReply:
		return l.T("menu.reply")
	case actionCopySelected:
		return l.T("menu.copy_selected")
	case actionCopyText:
		return l.T("menu.copy_text")
	case actionCopyLink:
		if p.kind == model.KindChannel {
			return l.T("menu.copy_post_link")
		}
		return l.T("menu.copy_message_link")
	case actionForward:
		return l.T("menu.forward")
	case actionForwardSelected:
		return l.T("menu.forward_selected")
	case actionDelete:
		return l.T("menu.delete")
	case actionDeleteSelected:
		return l.T("menu.delete_selected")
	case actionSelect:
		return l.T("menu.select")
	case actionClearSelection:
		return l.T("menu.clear_selection")
	case actionRead:
		return l.T("menu.read")
	case actionEdits:
		return l.T("menu.edits")
	case actionFilter:
		return l.T("menu.filter")
	case actionRepeat:
		return l.T("menu.repeat")
	case actionSaveHTML:
		return l.T("rich.save_html")
	case actionTranslate:
		if m, ok := p.menuMessage(); ok && p.menuSelectedText(m) != "" {
			return l.T("menu.translate_selected")
		}
		return l.T("menu.translate")
	case actionReacted:
		total := 0
		for _, r := range p.messageMenu.reactedOf {
			total += r.Count
		}
		return l.Count("menu.reacted", total, nil)
	case actionEmojiPacks:
		found := p.messageMenu.packs.found
		text := l.Count("menu.emoji_packs", len(found.refs), nil)
		if len(found.refs) == 1 && found.title != "" {
			text = l.Format("menu.emoji_pack", map[string]string{"name": found.title})
		}
		// Telegram's texts mark the pack's name bold, which a label cannot.
		return strings.ReplaceAll(text, "**", "")
	}
	return ""
}

func menuIcon(a menuAction) wdk.IconWidget {
	switch a {
	case actionReply:
		return iconReply
	case actionCopySelected, actionCopyText:
		return iconCopy
	case actionCopyLink:
		return iconLink
	case actionForward, actionForwardSelected:
		return iconForward
	case actionDelete, actionDeleteSelected:
		return iconDelete
	case actionSelect, actionClearSelection:
		return iconSelect
	case actionReacted:
		return iconReacted
	case actionRead:
		return iconRead
	case actionEdits:
		return iconHistory
	case actionFilter:
		return iconFilter
	case actionTranslate:
		return iconTranslate
	case actionRepeat:
		return iconRepeat
	case actionSaveHTML:
		return iconDownload
	}
	return iconEmoji
}

// menuLayout draws the menu over the chat page, whose history starts at
// top, while it is open or closing.
func (p *chatPage) menuLayout(gtx layout.Context, l localization.Catalog) {
	m := &p.messageMenu
	size := gtx.Constraints.Max
	if m.open {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press})
			if !ok {
				break
			}
			e, ok := ev.(pointer.Event)
			if !ok {
				continue
			}
			p.closeMenu()
			if e.Source == pointer.Mouse && e.Buttons == pointer.ButtonSecondary && e.Position.Y >= float32(m.top) {
				// A right click elsewhere opens the menu there.
				p.openMenu(gtx, e.Position.Sub(f32.Pt(0, float32(m.top))), m.top)
			}
			gtx.Execute(op.InvalidateCmd{})
		}
		for m.open {
			ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State == key.Press {
				p.closeMenu()
			}
		}
	}
	if m.open {
		msg, ok := p.menuMessage()
		if !ok {
			p.closeMenu()
		} else {
			m.shown = p.menuActions(msg)
			m.reactions.shown = p.menuReactions(msg)
			m.reactedOf = msg.Reactions
			m.rect, m.corner = menuRect(gtx, &m.menu, m.at, size, m.shown, m.reactions.height(gtx))
		}
	}
	if m.open {
		// Clicks around the menu close it, and take nothing else.
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &m.dismiss)
		area.Pop()
	}
	shown := m.shown
	radius := gtx.Dp(12)
	if !m.open {
		// A closing menu lets clicks through to the messages under it.
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	m.menu.Layout(gtx, m.open, m.rect, m.corner, radius, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		menuSize := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: menuSize}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, p.menuBackdrop(), menuSize, m.menu.bounds.Min, sc.SurfaceContainerHigh, radius)
		event.Op(gtx.Ops, &m.panel)
		y := gtx.Dp(menuPadding)
		if targetStrip := m.reactions.height(gtx); targetStrip > 0 {
			strip := max(0, targetStrip+menuSize.Y-m.rect.Dy())
			m.reactions.layoutHeight(gtx, p, menuSize.X, strip, p.animate)
			y = strip
		}
		for _, a := range shown {
			height := gtx.Dp(menuItemHeight)
			if a == actionEmojiPacks {
				// A separator sets the packs apart, as in Telegram Desktop.
				line := op.Offset(image.Pt(0, y+gtx.Dp(4))).Push(gtx.Ops)
				fillRect(gtx, sc.OutlineVariant, image.Pt(menuSize.X, gtx.Dp(1)))
				line.Pop()
				y += gtx.Dp(9)
				height = gtx.Dp(menuPackHeight)
			}
			inRect(gtx, image.Rect(0, y, menuSize.X, y+height), func(gtx layout.Context) layout.Dimensions {
				row := gtx.Constraints.Max
				text := p.menuLabel(a, l)
				style := surfaceStyle{background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: text}
				return m.items[a].Layout(gtx, row, style, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return menuIcon(a)(gtx, sc.SurfaceVariant.OnColor) }),
							layout.Rigid(layout.Spacer{Width: 12}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min = image.Point{}
									if a == actionEmojiPacks {
										return label(gtx, text, token.TypestyleBodySmall, sc.Surface.OnColor, 2)
									}
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

// menuRect is where a menu with actions, under a strip of reactions strip
// high, opens from at in a page of size: below and after it, or on the sides
// where there is room.
func menuRect(gtx layout.Context, menu *contextMenu, at, size image.Point, actions []menuAction, strip int) (image.Rectangle, menuCorner) {
	margin := gtx.Dp(8)
	w := min(gtx.Dp(menuWidth), max(0, size.X-2*margin))
	h := 2*gtx.Dp(menuPadding) + strip
	if strip > 0 {
		// The strip replaces the padding at the top.
		h -= gtx.Dp(menuPadding)
	}
	for _, a := range actions {
		if a == actionEmojiPacks {
			h += gtx.Dp(9) + gtx.Dp(menuPackHeight)
		} else {
			h += gtx.Dp(menuItemHeight)
		}
	}
	h = min(h, max(0, size.Y-2*margin))
	return menu.Place(gtx, at, size, image.Pt(w, h))
}

// customEmojiSource is a store that finds custom emoji documents, and so
// their sets.
type customEmojiSource interface {
	CustomEmoji(context.Context, int64) (model.Message, error)
}

// emojiPacks are the sets of the custom emoji of a message; title is the
// only one's name.
type emojiPacks struct {
	refs  []model.StickerSetRef
	title string
}

// emojiPackLookup finds the sets of a message's custom emoji while its
// menu is open, as Telegram Desktop's menu names them.
type emojiPackLookup struct {
	id      model.MessageID
	found   emojiPacks
	results chan emojiPacks
	cancel  context.CancelFunc
	// titles are the names of the sets already looked up.
	titles map[model.StickerSetRef]string
}

func (e *emojiPackLookup) stop() {
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.results = nil
}

// start looks up the sets of m's custom emoji.
func (e *emojiPackLookup) start(p *chatPage, m model.Message) {
	e.stop()
	e.id, e.found = m.Key.MessageID, emojiPacks{}
	source, ok := p.source.(customEmojiSource)
	if !ok {
		return
	}
	var ids []int64
	seen := map[int64]bool{}
	for _, part := range messageParts(m) {
		for _, entity := range part.Entities {
			if entity.Kind == "emoji" && entity.DocumentID != 0 && !seen[entity.DocumentID] && len(ids) < 100 {
				seen[entity.DocumentID] = true
				ids = append(ids, entity.DocumentID)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	sets, _ := p.source.(model.StickerSetStore)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	e.cancel = cancel
	e.results = make(chan emojiPacks, 1)
	results, known := e.results, map[model.StickerSetRef]string{}
	for ref, title := range e.titles {
		known[ref] = title
	}
	go func() {
		defer cancel()
		var found emojiPacks
		for _, id := range ids {
			doc, err := source.CustomEmoji(ctx, id)
			if err != nil || doc.Media == nil || doc.Media.StickerSet == nil {
				continue
			}
			ref := *doc.Media.StickerSet
			duplicate := false
			for _, r := range found.refs {
				duplicate = duplicate || r == ref
			}
			if !duplicate {
				found.refs = append(found.refs, ref)
			}
		}
		if len(found.refs) == 1 {
			found.title = known[found.refs[0]]
			if found.title == "" && sets != nil {
				// Read only: the set's name, which Telegram Desktop shows
				// when it knows the set.
				if set, err := sets.StickerSet(ctx, found.refs[0]); err == nil {
					found.title = set.Title
				}
			}
		}
		results <- found
		p.invalidate()
	}()
}

// update takes a finished lookup.
func (e *emojiPackLookup) update() {
	if e.results == nil {
		return
	}
	select {
	case found := <-e.results:
		e.found, e.results = found, nil
		if len(found.refs) == 1 && found.title != "" {
			if e.titles == nil {
				e.titles = map[model.StickerSetRef]string{}
			}
			e.titles[found.refs[0]] = found.title
		}
	default:
	}
}
