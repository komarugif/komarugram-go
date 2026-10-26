// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gio-mw/widget/button"

	"gioui.org/layout"

	"komarugram/internal/messenger/model"
)

// chatReturn is a step of the way back through chats: chat was opened
// from from, as the chat of a sender whose avatar was clicked from their
// group, or a channel's discussion from the channel; thread, when set, is
// the comments or the forum's topic open over from, which the way back
// opens again.
type chatReturn struct {
	from   model.Chat
	chat   int64
	thread *commentsView
}

// returnsMax is how many steps back the way keeps.
const returnsMax = 32

// openReturning opens chat from from, or from thread over it, with a back
// button in its header that goes back there; the way back goes on from
// from, when it was opened so too. From thread, chat may be from itself,
// as the channel of the comments: they close over it.
func (a *App) openReturning(chat, from model.Chat, thread *commentsView) {
	a.openReturningAt(chat, from, thread, 0)
}

// openReturningAt is openReturning with chat open at message, 0 for where
// it would open: a channel's post a message was forwarded from.
func (a *App) openReturningAt(chat, from model.Chat, thread *commentsView, message model.MessageID) {
	if n := len(a.returns); n > 0 && a.returns[n-1].chat != from.ID {
		a.returns = nil
	}
	a.open(chatPick{ID: chat.ID, Chat: &chat, Message: message})
	if thread != nil && a.thread == thread {
		a.closeComments()
	}
	if chat.ID == from.ID && thread == nil {
		return
	}
	if len(a.returns) == returnsMax {
		a.returns = append(a.returns[:0], a.returns[1:]...)
	}
	a.returns = append(a.returns, chatReturn{from: from, chat: chat.ID, thread: thread})
	if a.returnBack == nil {
		a.returnBack = button.Text()
	}
	a.window.Invalidate()
}

// returnButton is the back button of the open chat's header, nil when it
// was not opened from another.
func (a *App) returnButton() *button.Button {
	if len(a.returns) == 0 {
		return nil
	}
	return a.returnBack
}

// updateReturn goes back a step when the header's back button is clicked,
// and forgets the way back once a chat off it is open.
func (a *App) updateReturn(gtx layout.Context) {
	n := len(a.returns)
	if n > 0 && a.selected != a.returns[n-1].chat {
		a.returns, n = nil, 0
	}
	if n > 0 && a.returnBack.Clicked(gtx) {
		a.goBack()
	}
}

// goBack goes back a step, to the chat the open one was opened from, where
// it was left: its history keeps its place as when another chat is opened.
func (a *App) goBack() {
	n := len(a.returns)
	if n == 0 {
		return
	}
	r := a.returns[n-1]
	a.returns = a.returns[:n-1]
	if a.info != nil && a.info.visible {
		a.info.Close()
	}
	a.open(chatPick{ID: r.from.ID, Chat: &r.from})
	if r.thread != nil && a.comments != nil {
		a.thread = r.thread
	}
}
