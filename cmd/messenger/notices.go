// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"strconv"
	"strings"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/ui"
	"komarugram/internal/notify"
)

// notice shows a notification of a message that came to the account of s.
func (h *accountWindows) notice(s *accountSession, n model.MessageNotice) {
	id := s.accountID()
	if h.notifier == nil || id == "" {
		return
	}
	app := s.app.Load()
	if s.window.Load() == nil {
		app = nil
	}
	chat := n.Chat.ID
	showNotice(h.notifier, h.preferences.Global(), id, viewOf(app, s.locked.Load()), n, func(token string) { h.openChat(id, chat, token) })
}

// noticeView is what the account's window shows: the chat open while it
// has the focus, and whether it is locked.
type noticeView struct {
	chat            int64
	focused, locked bool
}

// viewOf is what app shows; nil for no window.
func viewOf(app *ui.App, locked bool) noticeView {
	v := noticeView{locked: locked}
	if app != nil {
		v.chat, v.focused = app.Showing()
	}
	return v
}

// showNotice shows the notification of n to account, if the settings ask
// for one; a click opens it.
func showNotice(notifier notify.Notifier, g preferences.Global, account string, view noticeView, n model.MessageNotice, open func(token string)) {
	if note, ok := noticeFor(g, account, view, n, localization.For(g.Language)); ok {
		note.Open = open
		notifier.Show(note)
	}
}

// noticeFor is the notification of n to account, as the settings ask: none
// of a muted chat, nor of the chat the window shows while it has the focus.
func noticeFor(g preferences.Global, account string, view noticeView, n model.MessageNotice, l localization.Catalog) (notify.Notification, bool) {
	p := g.Notify
	switch {
	case !p.Desktop || n.Chat.Muted || !noticeKind(p, n.Chat.Kind),
		!p.AllAccounts && account != g.LastAccountID,
		view.focused && view.chat == n.Chat.ID:
		return notify.Notification{}, false
	}
	title, body := noticeText(n, p, view.locked, l)
	return notify.Notification{
		Title: title, Body: body,
		Sound: p.Sound && !n.Silent,
		Tag:   noticeTag(account, n.Chat.ID),
	}, true
}

// noticeTag names a notification by its account and chat; openNotice reads
// it back.
func noticeTag(account string, chat int64) string {
	return account + "/" + strconv.FormatInt(chat, 10)
}

// openNotice opens the chat of the notification tagged tag, for a
// notification clicked outside the process: on Haiku a click starts the
// messenger again, with -notified, and that start hands the tag over.
func (h *accountWindows) openNotice(tag string) {
	i := strings.LastIndexByte(tag, '/')
	if i < 0 {
		return
	}
	chat, err := strconv.ParseInt(tag[i+1:], 10, 64)
	if err != nil {
		return
	}
	h.openChat(tag[:i], chat, "")
}

func noticeKind(p preferences.Notify, kind model.ChatKind) bool {
	switch kind {
	case model.KindGroup:
		return p.Groups
	case model.KindChannel:
		return p.Channels
	}
	return p.Private
}

// noticeText is what a notification says: as much as the settings let it
// show, and nothing of the message while the window is locked.
func noticeText(n model.MessageNotice, p preferences.Notify, locked bool, l localization.Catalog) (title, body string) {
	if locked || !p.Name {
		return l.T("app.title"), l.T("notify.new_message")
	}
	if !p.Text {
		return n.Chat.Title, l.T("notify.new_message")
	}
	if n.Sender != "" {
		return n.Chat.Title, n.Sender + ": " + n.Text
	}
	return n.Chat.Title, n.Text
}

// openChat brings the account's window, opened again if it was closed, and
// opens chat in it.
func (h *accountWindows) openChat(id string, chat int64, token string) {
	h.mu.Lock()
	window, session := h.windows[id], h.sessions[id]
	h.mu.Unlock()
	if session == nil {
		return
	}
	if app := session.app.Load(); app != nil && window != nil {
		app.OpenChat(chat)
		h.remember(id)
		window.Activate(token)
		return
	}
	session.openChat.Store(chat)
	h.Open(id)
}
