// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// senderResult is the chat with the sender of message, whose avatar was
// clicked in the group from, or in thread, the comments or the topic open
// over from, or why there is none.
type senderResult struct {
	from    model.Chat
	thread  *commentsView
	message model.Message
	chat    model.Chat
	err     error
}

// openSender asks for the chat with the sender of the group message m,
// shown in thread when it is set, which takeSender opens.
func (a *App) openSender(source model.SenderChatSource, m model.Message, thread *commentsView) {
	from, ok := a.selectedChat()
	if !ok || a.senderBusy || thread != a.thread || thread == nil && from.ID != m.Key.ChatID {
		return
	}
	a.senderBusy = true
	results, invalidate := a.senders, a.window.Invalidate
	go func() {
		defer crash.Recover("sender chat", func(e *crash.Panic) {
			results <- senderResult{from: from, thread: thread, message: m, err: e}
			invalidate()
		})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		chat, err := source.SenderChat(ctx, m)
		results <- senderResult{from: from, thread: thread, message: m, chat: chat, err: err}
		invalidate()
	}()
}

// takeSender opens the chat that came, with a way back to the group: a
// channel's, which the account need not follow, and a user's when the
// account has it; otherwise only the user's info, which goes to it. A
// channel the account may not read, nor has cached, has only its info. A
// failure is told; a chat that came after the group was left is not
// opened.
func (a *App) takeSender(r senderResult) {
	a.senderBusy = false
	if a.selected != r.from.ID || a.thread != r.thread {
		return
	}
	l := a.catalog()
	page := a.history
	if r.thread != nil {
		page = a.comments
	}
	switch {
	case errors.Is(r.err, model.ErrChatInaccessible) && r.chat.ID != 0 && a.info != nil:
		a.info.OpenClosed(r.chat, l.T("links.channel_not_accessible"))
		return
	case errors.Is(r.err, model.ErrLinkNotFound) && model.IsChannelID(r.message.SenderID):
		page.toast.Show(l.T("links.channel_not_accessible"))
		return
	case errors.Is(r.err, model.ErrLinkNotFound):
		page.toast.Show(l.Format("links.username_not_found", map[string]string{"user": r.message.SenderName}))
		return
	case r.err != nil:
		page.toast.Show(mediaErrorText(r.err))
		return
	}
	personal := r.chat.Kind == model.KindUser || r.chat.Kind == model.KindBot
	if !personal || a.hasChat(r.chat.ID) || a.info == nil {
		a.openSenderChat(r)
		return
	}
	// No chat with them yet: their info, over the group, goes to one.
	a.info.OpenOver(r.chat, func() { a.openSenderChat(r) })
}

// hasChat tells whether the account has a chat with id in its list.
func (a *App) hasChat(id int64) bool {
	for _, c := range a.store.Chats() {
		if c.ID == id {
			return true
		}
	}
	return false
}

// openSenderChat opens the chat with the sender r found, with a way back
// to the group.
func (a *App) openSenderChat(r senderResult) {
	a.openReturning(r.chat, r.from, r.thread)
}
