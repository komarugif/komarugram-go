// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/motion"
)

func returnApp(t *testing.T) (*App, *mockstore.Store) {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	a := New(w, store, Services{})
	t.Cleanup(func() {
		a.Close()
		w.Motion.Close()
	})
	return a, store
}

func (a *App) testUpdate() {
	a.Update(layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(900, 700))})
}

// The way back goes step by step through the chats opened from others:
// from a sender's chat to the discussion, and on to its channel. A chat
// opened off the way forgets it.
func TestChatReturnSteps(t *testing.T) {
	a, _ := returnApp(t)
	const channel, group, user = 4, 3, 2
	chat := func(id int64) model.Chat {
		for _, c := range a.store.Chats() {
			if c.ID == id {
				return c
			}
		}
		t.Fatalf("no chat %d", id)
		return model.Chat{}
	}
	a.open(chatPick{ID: channel})
	a.openReturning(chat(group), chat(channel), nil)
	a.testUpdate()
	a.openReturning(chat(user), chat(group), nil)
	a.testUpdate()
	if a.selected != user || a.returnButton() == nil || len(a.returns) != 2 {
		t.Fatalf("selected %d, back %v, %d steps", a.selected, a.returnButton() != nil, len(a.returns))
	}
	a.goBack()
	a.testUpdate()
	if a.selected != group || a.returnButton() == nil {
		t.Fatalf("first step: selected %d, back %v", a.selected, a.returnButton() != nil)
	}
	a.goBack()
	a.testUpdate()
	if a.selected != channel || a.returnButton() != nil || len(a.returns) != 0 {
		t.Fatalf("second step: selected %d, back %v", a.selected, a.returnButton() != nil)
	}

	a.openReturning(chat(group), chat(channel), nil)
	a.testUpdate()
	a.open(chatPick{ID: 5})
	a.testUpdate()
	if a.returnButton() != nil || len(a.returns) != 0 {
		t.Fatal("another chat kept the way back")
	}
	// Opened from a chat off the way, a chat starts a new one.
	a.openReturning(chat(group), chat(channel), nil)
	a.testUpdate()
	a.openReturning(chat(user), chat(5), nil)
	if len(a.returns) != 1 || a.returns[0].from.ID != 5 {
		t.Fatalf("steps %+v", a.returns)
	}
}

// waitSender lays out frames until the sender asked for comes.
func (a *App) waitSender(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for a.senderBusy && time.Now().Before(deadline) {
		a.testUpdate()
		time.Sleep(5 * time.Millisecond)
	}
	if a.senderBusy {
		t.Fatal("no answer")
	}
}

// An avatar in the comments opens its sender's chat, and the way back
// opens the comments again; the channel's own, of the post, closes them
// over the channel, and goes back to them too.
func TestCommentsSenderReturns(t *testing.T) {
	a, store := returnApp(t)
	const channel = 4
	a.open(chatPick{ID: channel})
	var post model.Message
	for _, m := range store.History(channel).Messages {
		if m.CommentsOpen && m.Comments >= 2 {
			post = m
			break
		}
	}
	a.openComments(post)
	view := a.thread
	if view == nil || a.comments.openSender == nil {
		t.Fatal("the comments' avatars open nothing")
	}
	messages := store.History(view.chat.ID).Messages
	root, comment := messages[0], messages[1]

	a.comments.openSender(comment)
	a.waitSender(t)
	if a.selected != comment.SenderID || a.returnButton() == nil {
		t.Fatalf("comment: selected %d, back %v", a.selected, a.returnButton() != nil)
	}
	a.goBack()
	if a.selected != channel || a.thread != view {
		t.Fatalf("back: selected %d, comments %v", a.selected, a.thread == view)
	}

	a.comments.openSender(root)
	a.waitSender(t)
	if a.selected != channel || a.thread != nil || a.returnButton() == nil {
		t.Fatalf("channel: selected %d, comments %v, back %v", a.selected, a.thread != nil, a.returnButton() != nil)
	}
	a.goBack()
	if a.selected != channel || a.thread != view {
		t.Fatalf("back to the comments: selected %d, comments %v", a.selected, a.thread == view)
	}
}

// A forum opened from another chat has the back button in its header,
// which takes its clicks from the header's own.
func TestForumHeaderBack(t *testing.T) {
	a, _ := returnApp(t)
	const group = 3
	var forum, from model.Chat
	for _, c := range a.store.Chats() {
		switch c.ID {
		case mockstore.DemoForum:
			forum = c
		case group:
			from = c
		}
	}
	a.open(chatPick{ID: group})
	a.openReturning(forum, from, nil)
	var router input.Router
	info := false
	frame := func() {
		gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(700, 500)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		a.updateReturn(gtx)
		info = info || a.forum.header.Clicked(gtx)
		if a.selected == forum.ID {
			a.layoutForum(gtx, forum, localization.For("ru"))
		}
		router.Frame(gtx.Ops)
	}
	frame()
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(28, 28)})
		frame()
	}
	if a.selected != group || info {
		t.Fatalf("selected %d, info %v", a.selected, info)
	}
}

// The comments' back button draws the channel in the frame of the click,
// not an empty page before it.
func TestCommentsBackDrawsChannel(t *testing.T) {
	a, store := returnApp(t)
	const channel = 4
	a.open(chatPick{ID: channel})
	for _, m := range store.History(channel).Messages {
		if m.CommentsOpen {
			a.openComments(m)
			break
		}
	}
	if a.thread == nil {
		t.Fatal("no comments opened")
	}
	var router input.Router
	frame := func() {
		gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(500, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		a.layoutPage(gtx)
		router.Frame(gtx.Ops)
	}
	frame()
	// What the channel's history laid out last, before the comments.
	a.history.list.Position.Count = 0
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(28, 28)})
		frame()
	}
	if a.thread != nil {
		t.Fatal("the comments stayed open")
	}
	if a.history.list.Position.Count == 0 {
		t.Fatal("the frame of the click drew no channel")
	}
}

// The channel's Discussion button opens its group with a way back to the
// channel.
func TestDiscussionReturns(t *testing.T) {
	a, store := returnApp(t)
	const channel = 4
	a.open(chatPick{ID: channel})
	group, err := store.Discussion(t.Context(), channel)
	if err != nil {
		t.Fatal(err)
	}
	c := a.history.composer
	c.chat = channel
	c.discussions <- discussionResult{from: channel, chat: group}
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(400, 56)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	c.layoutReadOnly(gtx, a.history, localization.For("ru"))
	a.testUpdate()
	if a.selected != group.ID || a.returnButton() == nil {
		t.Fatalf("selected %d, back %v", a.selected, a.returnButton() != nil)
	}
	a.goBack()
	if a.selected != channel {
		t.Fatalf("back to %d", a.selected)
	}
}
