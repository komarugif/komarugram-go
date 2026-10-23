// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"strings"
	"time"
	"unicode/utf8"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// The message field edits a message of the account's own as Telegram
// Desktop's does (history_widget.cpp, editMessage and saveEditMessage):
// "Edit" in the message's menu, or the Up arrow in an empty field, puts the
// message's text in the field, its formatting and custom emoji kept, with
// the strip over the field telling what is edited; Enter saves it, Escape
// or the strip's cross gives the field back what it had before.

// draftBefore is what the field had when an edit began, which it gets back
// once the edit ends.
type draftBefore struct {
	text     string
	entities []model.Entity
	reply    *model.Message
}

// editResult is what came of saving an edit.
type editResult struct {
	chat int64
	msg  model.Message
	err  error
}

// startEdit puts m, a message of chat, a chat of kind, in the field of
// chat to be edited, and gives the field the keyboard.
func (c *messageComposer) startEdit(gtx layout.Context, chat int64, m model.Message, kind model.ChatKind) {
	d := c.draft(chat)
	if d.sending {
		return
	}
	if d.editing == nil {
		d.before = &draftBefore{text: d.editor.Text(), entities: d.entities, reply: d.reply}
	}
	edited := m
	d.editing, d.editKind = &edited, kind
	d.reply = nil
	d.editor.SetText(m.Text)
	n := utf8.RuneCountInString(m.Text)
	d.editor.SetCaret(n, n)
	d.text = m.Text
	d.entities = model.EditableEntities(m)
	d.pending, d.err = nil, nil
	c.fieldMenu.open = false
	if c.source != nil {
		gtx.Execute(key.FocusCmd{Tag: &d.editor})
	}
	gtx.Execute(op.InvalidateCmd{})
}

// endEdit gives the field of chat back what it had before the edit.
func (c *messageComposer) endEdit(chat int64) {
	d := c.draft(chat)
	if d.editing == nil {
		return
	}
	before := d.before
	if before == nil {
		before = &draftBefore{}
	}
	d.editing, d.before = nil, nil
	d.editor.SetText(before.text)
	n := utf8.RuneCountInString(before.text)
	d.editor.SetCaret(n, n)
	d.text, d.entities, d.reply = before.text, before.entities, before.reply
	d.pending, d.err = nil, nil
	d.sending = false
}

// saveEdit saves the text of the field of chat as the message edited, as
// Telegram Desktop's saveEditMessage: a text too long is told and kept, a
// text that did not change ends the edit, and an empty one of a message
// without media asks to delete the message instead.
func (c *messageComposer) saveEdit(chat int64) {
	d := c.draft(chat)
	if d.editing == nil || d.sending {
		return
	}
	editor, ok := c.source.(model.MessageEditor)
	if !ok {
		return
	}
	m := *d.editing
	text := d.editor.Text()
	entities := model.SortEntities(d.entities)
	if !model.Editable(m, d.editKind, time.Now(), editor.EditTimeLimit()) {
		d.err = model.ErrEditForbidden
		return
	}
	if err := model.CheckEdit(m, text); errors.Is(err, model.ErrMessageEmpty) {
		c.askDelete = &m
		return
	} else if err != nil {
		d.err = err
		return
	}
	if model.SameText(m, text, entities) {
		// Telegram would answer MESSAGE_NOT_MODIFIED, which ends the edit.
		c.endEdit(chat)
		return
	}
	d.sending = true
	d.err = nil
	go func() {
		defer crash.Recover("edit", func(e *crash.Panic) {
			select {
			case c.edits <- editResult{chat: chat, msg: m, err: e}:
				c.invalidate()
			case <-c.ctx.Done():
			}
		})
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		err := editor.EditMessage(ctx, m.Key, text, entities)
		select {
		case c.edits <- editResult{chat: chat, msg: m, err: err}:
			c.invalidate()
		case <-c.ctx.Done():
		}
	}()
}

// editDone takes what came of saving an edit: done, or nothing changed,
// ends it; a failure is told, and the edit stays.
func (c *messageComposer) editDone(r editResult) {
	d := c.draft(r.chat)
	if d.editing == nil || d.editing.Key != r.msg.Key {
		return
	}
	d.sending = false
	switch {
	case r.err == nil, errors.Is(r.err, model.ErrMessageNotModified):
		c.endEdit(r.chat)
	case errors.Is(r.err, context.Canceled):
	default:
		d.err = r.err
	}
}

// editErrorText is the text of an edit's failure, Telegram Desktop's,
// "" for an error not of an edit.
func editErrorText(err error, l localization.Catalog) string {
	var long *model.EditTooLong
	switch {
	case errors.As(err, &long):
		return l.Count("composer.edit_limit", long.Over, nil)
	case errors.Is(err, model.ErrEditForbidden), errors.Is(err, model.ErrMessageEmpty), errors.Is(err, model.ErrMessageNotModified):
		return l.T("composer.edit_error")
	}
	return ""
}

// lastEditable is the last message of the history the account may edit,
// which the Up arrow in an empty field edits, as in Telegram Desktop.
func (p *chatPage) lastEditable() (model.Message, bool) {
	for i := len(p.messages) - 1; i >= 0; i-- {
		if m, ok := p.editablePart(p.messages[i]); ok {
			return m, true
		}
	}
	return model.Message{}, false
}

// editablePart is the part of m that its edit edits: m, or the part of an
// album that has the caption, when the account may edit it.
func (p *chatPage) editablePart(m model.Message) (model.Message, bool) {
	editor, ok := p.source.(model.MessageEditor)
	if !ok || p.composer == nil || p.composer.source == nil || p.frozen.Frozen() {
		return model.Message{}, false
	}
	parts := messageParts(m)
	part := parts[0]
	for _, one := range parts {
		if one.Text != "" {
			part = one
			break
		}
	}
	if !model.Editable(part, p.kind, time.Now(), editor.EditTimeLimit()) {
		return model.Message{}, false
	}
	return part, true
}

// updateEdit follows the message edited in the history: it shows the
// message as it is now, and an edit of a message deleted ends, as Telegram
// Desktop tells it.
func (c *messageComposer) updateEdit(chat int64, p *chatPage, l localization.Catalog) {
	d := c.drafts[chat]
	if d == nil || d.editing == nil || p == nil {
		return
	}
	for _, m := range p.messages {
		for _, part := range messageParts(m) {
			if part.Key != d.editing.Key {
				continue
			}
			if part.Deleted {
				c.endEdit(chat)
				p.toast.Show(l.T("composer.edit_deleted"))
				return
			}
			if part.ContentRevision != d.editing.ContentRevision && !d.sending {
				fresh := part
				d.editing = &fresh
			}
		}
	}
}

// editKeys takes the keys of the field of chat before the field does: the
// Up arrow in an empty field edits the last message, Escape ends an edit,
// and the formatting shortcuts format what is selected.
func (c *messageComposer) editKeys(gtx layout.Context, chat int64, d *messageDraft, p *chatPage) {
	if d.sending || c.source == nil || !gtx.Focused(&d.editor) {
		return
	}
	filters := formatShortcuts(&d.editor)
	up := d.editing == nil && d.editor.Len() == 0 && p != nil
	if up {
		filters = append(filters, key.Filter{Focus: &d.editor, Name: key.NameUpArrow})
	}
	if d.editing != nil && !c.pickerOpen && !c.attachOpen && c.form == 0 && !c.fieldMenu.open {
		filters = append(filters, key.Filter{Focus: &d.editor, Name: key.NameEscape})
	}
	ef := make([]event.Filter, len(filters))
	for i := range filters {
		ef[i] = filters[i]
	}
	for {
		ev, ok := gtx.Event(ef...)
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch {
		case e.Name == key.NameUpArrow && e.Modifiers == 0 && up:
			if m, ok := p.lastEditable(); ok {
				c.startEdit(gtx, chat, m, p.kind)
			}
		case e.Name == key.NameEscape && d.editing != nil:
			c.endEdit(chat)
			gtx.Execute(op.InvalidateCmd{})
		default:
			if a, ok := formatOfKey(e); ok {
				c.applyFormat(gtx, d, a)
			}
		}
	}
}

// applyFormat applies a to what is selected in the field of d.
func (c *messageComposer) applyFormat(gtx layout.Context, d *messageDraft, a formatAction) {
	text := d.editor.Text()
	s, e := d.editor.Selection()
	s, e = min(s, e), max(s, e)
	if a.kind == model.EntityURL {
		c.link.open(gtx, d, text, s, e)
		return
	}
	if s == e {
		return
	}
	start, end := runeToU16(text, s), runeToU16(text, e)
	if a.kind == "" {
		d.entities = clearFormat(d.entities, start, end)
	} else {
		d.entities = toggleFormat(d.entities, a.kind, start, end)
	}
	d.pending, d.err = nil, nil
	gtx.Execute(op.InvalidateCmd{})
}

// fieldMenu is the menu a right click on the field opens over what is
// selected in it, Telegram Desktop's Formatting submenu: its items format
// it, with their shortcuts beside them.
type fieldMenu struct {
	menu    contextMenu
	open    bool
	at      image.Point
	items   [len(formatActions)]surface
	dismiss byte
}

// fieldMenuWidth is the width of the field's menu, which has the
// shortcuts beside the texts.
const fieldMenuWidth = 320

// openFieldMenu opens the menu at pos in the field, when text is selected.
func (c *messageComposer) openFieldMenu(gtx layout.Context, d *messageDraft, pos image.Point) {
	if s, e := d.editor.Selection(); s == e {
		return
	}
	c.fieldMenu = fieldMenu{open: true, at: c.fieldOrigin.Add(pos)}
	gtx.Execute(op.InvalidateCmd{})
}

// layoutFieldMenu draws the field's menu in the composer, of size, and does
// what is chosen in it.
func (c *messageComposer) layoutFieldMenu(gtx layout.Context, d *messageDraft, p *chatPage, l localization.Catalog) {
	m := &c.fieldMenu
	size := gtx.Constraints.Max
	if m.open {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press}, key.Filter{Name: key.NameEscape})
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State != key.Press {
				continue
			}
			m.open = false
		}
		for i := range m.items {
			if m.items[i].Clicked(gtx) && m.open {
				m.open = false
				c.applyFormat(gtx, d, formatActions[i])
				gtx.Execute(key.FocusCmd{Tag: &d.editor})
			}
		}
	}
	if m.open {
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &m.dismiss)
		area.Pop()
	}
	w := min(gtx.Dp(fieldMenuWidth), max(0, size.X-2*gtx.Dp(8)))
	h := 2*gtx.Dp(menuPadding) + len(formatActions)*gtx.Dp(menuItemHeight)
	rect, corner := m.menu.Place(gtx, m.at, size, image.Pt(w, h))
	radius := gtx.Dp(12)
	m.menu.Layout(gtx, m.open, rect, corner, radius, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		menuSize := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: menuSize}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, p.menuBackdrop(), menuSize, m.menu.bounds.Min, sc.SurfaceContainerHigh, radius)
		event.Op(gtx.Ops, &m.menu)
		y := gtx.Dp(menuPadding)
		for i, a := range formatActions {
			text := l.T(a.label)
			if a.kind == model.EntityURL && d != nil {
				s, e := d.editor.Selection()
				all := d.editor.Text()
				if linkAt(d.entities, runeToU16(all, min(s, e)), runeToU16(all, max(s, e))) != "" {
					text = l.T("format.link_edit")
				}
			}
			inRect(gtx, image.Rect(0, y, menuSize.X, y+gtx.Dp(menuItemHeight)), func(gtx layout.Context) layout.Dimensions {
				row := gtx.Constraints.Max
				style := surfaceStyle{background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: text}
				return m.items[i].Layout(gtx, row, style, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, row.Y)
						gtx.Constraints.Max.Y = row.Y
						// The shortcut at the end, the text in what is left.
						shortcut := op.Record(gtx.Ops)
						free := gtx
						free.Constraints.Min = image.Point{}
						keys := label(free, formatShortcutText(a), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
						keysCall := shortcut.Stop()
						at := image.Pt(gtx.Constraints.Max.X-keys.Size.X, (row.Y-keys.Size.Y)/2)
						offset := op.Offset(at).Push(gtx.Ops)
						keysCall.Add(gtx.Ops)
						offset.Pop()
						room := gtx
						room.Constraints = layout.Exact(image.Pt(max(0, at.X-gtx.Dp(12)), row.Y))
						layout.W.Layout(room, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min = image.Point{}
							return label(gtx, text, token.TypestyleBodyMedium, sc.Surface.OnColor, 1)
						})
						return layout.Dimensions{Size: gtx.Constraints.Min}
					})
				})
			})
			y += gtx.Dp(menuItemHeight)
		}
		return layout.Dimensions{Size: menuSize}
	})
}

// linkDialog makes what is selected in the field a link, as Telegram
// Desktop's box for creating a link: its text and its address.
type linkDialog struct {
	modal        modal
	text, url    *textField
	create, drop surface
	// draft is the field the link is made in, and start and end what was
	// selected in it, in runes.
	draft      *messageDraft
	start, end int
	focus      bool
	// edits tells that what was selected was a link already.
	edits bool
}

// open opens the dialog over [start, end) of text, the field of d's.
func (k *linkDialog) open(gtx layout.Context, d *messageDraft, text string, start, end int) {
	if k.text == nil {
		k.text, k.url = newTextField(0, ""), newTextField(0, "")
	}
	runes := []rune(text)
	k.text.editor.SetText(string(runes[start:end]))
	link := linkAt(d.entities, runeToU16(text, start), runeToU16(text, end))
	k.url.editor.SetText(link)
	k.edits = link != ""
	k.draft, k.start, k.end = d, start, end
	k.focus = true
	k.modal.Open()
	gtx.Execute(op.InvalidateCmd{})
}

// apply makes the field's text over the selection the dialog's text, a
// link to its address.
func (k *linkDialog) apply(c *messageComposer) {
	d := k.draft
	if d == nil || d.sending {
		return
	}
	label := k.text.Text()
	url := strings.TrimSpace(k.url.Text())
	if label == "" {
		label = url
	}
	if label == "" {
		return
	}
	if url != "" && !strings.Contains(url, "://") && !strings.HasPrefix(url, "tg:") && !strings.HasPrefix(url, "mailto:") {
		url = "https://" + url
	}
	n := d.editor.Len()
	start, end := min(k.start, n), min(k.end, n)
	d.editor.SetCaret(start, end)
	if d.editor.SelectedText() != label {
		d.editor.Insert(label)
		c.textChanged(d)
	}
	text := d.editor.Text()
	from := runeToU16(text, start)
	d.entities = setLink(d.entities, from, from+u16Len(label), url)
	end = start + utf8.RuneCountInString(label)
	d.editor.SetCaret(end, end)
	d.pending, d.err = nil, nil
}

// layoutLinkDialog draws the dialog of a link over the page.
func (c *messageComposer) layoutLinkDialog(gtx layout.Context, l localization.Catalog) {
	k := &c.link
	if !k.modal.Shown() {
		return
	}
	if !k.modal.closing {
		done := k.text.Submitted(gtx) || k.url.Submitted(gtx) || k.create.Clicked(gtx)
		if done {
			k.apply(c)
		}
		if done || k.drop.Clicked(gtx) {
			k.modal.Close()
			if k.draft != nil {
				gtx.Execute(key.FocusCmd{Tag: &k.draft.editor})
			}
		}
	}
	if k.focus && k.modal.Entered() {
		k.focus = false
		if k.text.Text() == "" {
			k.text.Focus(gtx)
		} else {
			k.url.Focus(gtx)
		}
	}
	title := l.T("format.link_title")
	if k.edits {
		title = l.T("format.link_edit_title")
	}
	k.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(400))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return k.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, title, token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return k.text.Layout(gtx, l.T("format.link_text"), false) }),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return k.url.Layout(gtx, l.T("format.link_url"), false) }),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &k.drop, l.T("history.cancel")) }),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return textButton(gtx, &k.create, l.T("format.link_create"))
							}),
						)
					})
				}),
			)
		}, defaultCardPadding)
	})
}
