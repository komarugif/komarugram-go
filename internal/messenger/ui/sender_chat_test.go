// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/motion"
)

// A member's avatar in a group opens their chat, and a channel's that
// posted there opens it; not the account's own, nor an anonymous admin's.
func TestSenderAvatarOpens(t *testing.T) {
	const group = -1000000000003
	p := &chatPage{kind: model.KindGroup, openSender: func(model.Message) {}}
	member := model.Message{Key: model.MessageKey{ChatID: group, MessageID: 1}, SenderID: 42, SenderName: "Анна"}
	channel := member
	channel.SenderID = -1000000000077
	for _, m := range []model.Message{member, channel} {
		if !p.senderOpens(m) {
			t.Fatalf("%+v does not open", m)
		}
	}
	own, admin, basic, service := member, member, member, member
	own.Outgoing = true
	admin.SenderID = group
	basic.SenderID = -5
	service.Kind = model.MessageService
	for _, m := range []model.Message{own, admin, basic, service} {
		if p.senderOpens(m) {
			t.Fatalf("%+v opens", m)
		}
	}
	p.kind = model.KindChannel
	if p.senderOpens(member) {
		t.Fatal("a channel's avatar opens")
	}
	p.kind, p.openSender = model.KindGroup, nil
	if p.senderOpens(member) {
		t.Fatal("opens with a store that finds no senders")
	}
}

// channelSenders is the demo whose groups' senders are all channel.
type channelSenders struct {
	*mockstore.Store
	channel model.Chat
	err     error
}

func (s *channelSenders) SenderChat(context.Context, model.Message) (model.Chat, error) {
	return s.channel, s.err
}

// A channel that posted in a group opens, followed or not; one the
// account may not read, nor has cached, opens only its info.
func TestSenderChannelOpens(t *testing.T) {
	channel := model.Chat{ID: -1000000000077, Kind: model.KindChannel, Title: "Канал"}
	for _, closed := range []bool{false, true} {
		store := &channelSenders{Store: mockstore.New(time.Now(), 0), channel: channel}
		if closed {
			store.err = model.ErrChatInaccessible
		}
		w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
		a := New(w, store, Services{})
		const group = 3
		a.open(chatPick{ID: group})
		a.history.openSender(model.Message{Key: model.MessageKey{ChatID: group, MessageID: 1}, SenderID: channel.ID})
		deadline := time.Now().Add(2 * time.Second)
		for a.senderBusy && time.Now().Before(deadline) {
			a.Update(layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(900, 700))})
			time.Sleep(5 * time.Millisecond)
		}
		switch {
		case !closed && (a.selected != channel.ID || a.info.visible || a.returnButton() == nil):
			t.Errorf("open: selected %d, info %v, back %v", a.selected, a.info.visible, a.returnButton() != nil)
		case closed && (a.selected != group || !a.info.visible || !a.info.closed || a.info.chat.ID != channel.ID || a.info.openChat != nil):
			t.Errorf("closed: selected %d, info of %d, visible %v, closed %v", a.selected, a.info.chat.ID, a.info.visible, a.info.closed)
		case closed && a.info.loading:
			t.Error("the info asks for the media of a chat it may not read")
		}
		a.Close()
		w.Motion.Close()
	}
}

// A click on a sender's avatar goes to the account's chat with them, with
// a way back to the group that another chat forgets; with no chat yet, it
// opens their info over the group, which goes to the chat.
func TestSenderChatAndBack(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	defer w.Motion.Close()
	a := New(w, store, Services{})
	defer a.Close()
	if a.history.openSender == nil {
		t.Fatal("the avatars open nothing")
	}
	const group = 3
	a.open(chatPick{ID: group})
	// Анна has a chat in the demo, Демонстрация has none.
	from := func(name string) model.Message {
		for _, m := range store.History(group).Messages {
			if !m.Outgoing && m.SenderName == name && m.Kind != model.MessageService {
				return m
			}
		}
		t.Fatalf("no message of %s", name)
		return model.Message{}
	}
	known, stranger := from("Анна"), from("Демонстрация")
	update := func() {
		a.Update(layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(900, 700))})
	}
	click := func(m model.Message, done func() bool) {
		t.Helper()
		a.history.openSender(m)
		deadline := time.Now().Add(2 * time.Second)
		for !done() && time.Now().Before(deadline) {
			update()
			time.Sleep(5 * time.Millisecond)
		}
		if a.senderBusy {
			t.Fatal("no answer")
		}
	}

	click(known, func() bool { return a.selected != group })
	if c, ok := a.selectedChat(); !ok || c.ID != 2 || c.Kind != model.KindUser {
		t.Fatalf("opened %+v, %v", c, ok)
	}
	if a.info.visible {
		t.Fatal("the info opened over a chat the account has")
	}
	if a.returnButton() == nil || len(a.returns) == 0 {
		t.Fatal("no way back")
	}
	a.goBack()
	if a.selected != group || a.returnButton() != nil {
		t.Fatalf("back: selected %d, button %v", a.selected, a.returnButton() != nil)
	}

	click(stranger, func() bool { return a.info.visible })
	if a.selected != group || a.info.chat.Title != stranger.SenderName || a.info.openChat == nil || len(a.returns) != 0 {
		t.Fatalf("selected %d, info of %+v, button %v", a.selected, a.info.chat, a.info.openChat != nil)
	}
	a.info.openChat()
	if c, ok := a.selectedChat(); !ok || c.ID != a.info.chat.ID || a.returnButton() == nil {
		t.Fatalf("went to %+v, %v, back %v", c, ok, a.returnButton() != nil)
	}
	// The info of a chat opened from its header has no such button.
	a.info.Open(a.info.chat)
	if a.info.openChat != nil {
		t.Fatal("the button stayed")
	}
	a.info.Close()

	a.open(chatPick{ID: 5})
	update()
	if len(a.returns) != 0 || a.returnButton() != nil {
		t.Fatal("another chat kept the way back")
	}
}

// A click on the avatar of a group's member opens their chat; the
// account's own messages have no avatar to click.
func TestSenderAvatarClick(t *testing.T) {
	for _, outgoing := range []bool{false, true} {
		h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
			for i := range messages {
				messages[i].SenderID, messages[i].Outgoing = 42, outgoing
			}
		})
		h.page.avatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(gtx.Dp(size), gtx.Dp(size))}
		}
		var opened []model.Message
		h.page.openSender = func(m model.Message) { opened = append(opened, m) }
		h.frames(2)
		// The avatar sticks to the bottom of the view, over the composer.
		for y := float32(h.page.composer.top - 2); y > 0 && len(opened) == 0; y -= 6 {
			h.press(pointer.ButtonPrimary, f32.Pt(33, y))
		}
		if !outgoing && (len(opened) != 1 || opened[0].SenderID != 42) {
			t.Fatalf("opened %+v", opened)
		}
		if outgoing && len(opened) != 0 {
			t.Fatalf("own message opened %+v", opened)
		}
	}
}
