// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/model"
)

// clickAcross clicks down message id's row, from its top, until opened is
// told of a chat or the row ends, and returns what was opened.
func (h *menuHarness) clickAcross(id model.MessageID, opened *[]model.Message) {
	h.t.Helper()
	center := h.messageAt(id)
	for y := center.Y - 60; y < center.Y+60 && len(*opened) == 0; y += 3 {
		h.press(pointer.ButtonPrimary, f32.Pt(center.X, y))
	}
}

// The line of whom a message was forwarded from opens their chat, by the
// way a group member's avatar does, when Telegram told who they are; one
// whose privacy leaves only a name opens nothing.
func TestForwardLineOpensItsSender(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[2].ForwardDate, m[2].ForwardFromID, m[2].ForwardName = time.Unix(10, 0), 77, "Pavel"
		m[3].ForwardDate, m[3].ForwardName = time.Unix(10, 0), "Hidden"
	})
	var opened []model.Message
	h.page.openSender = func(m model.Message) { opened = append(opened, m) }
	h.frames(3)
	h.clickAcross(3, &opened)
	if len(opened) != 1 || opened[0].SenderID != 77 || opened[0].SenderName != "Pavel" || opened[0].Key.MessageID != 3 {
		t.Fatalf("opened %+v", opened)
	}
	opened = nil
	h.clickAcross(4, &opened)
	if len(opened) != 0 {
		t.Fatalf("a forward of a hidden sender opened %+v", opened)
	}
}

// A forward's bubble is as wide as what it holds, with the avatar of whom
// it came from as without it: the name beside the avatar was Flexed, given
// the rest of the history's width as its minimum, and the bubble took all
// of it.
func TestForwardLineKeepsBubbleNarrow(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[2].ForwardDate, m[2].ForwardFromID, m[2].ForwardName = time.Unix(10, 0), 77, "Pavel"
		m[3].ForwardDate, m[3].ForwardName = time.Unix(10, 0), "Pavel"
	})
	h.page.avatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Dp(size), gtx.Dp(size))}
	}
	h.frames(3)
	with, without := h.page.rows[3], h.page.rows[4]
	if with == nil || without == nil {
		t.Fatal("rows not laid out")
	}
	// The avatar and the space after it, about 30 px, are all it may add.
	if with.bodySize.X > without.bodySize.X+40 {
		t.Fatalf("a forward with its sender's avatar is %d px wide, %d without it", with.bodySize.X, without.bodySize.X)
	}
}

// In Saved Messages a forward has the avatar of whom it came from beside
// it, a user's too, which opens their chat; one whose privacy leaves only
// a name has none.
func TestSavedForwardAvatar(t *testing.T) {
	user := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: 1}, ForwardDate: time.Unix(10, 0), ForwardFromID: 77, ForwardName: "Pavel"}
	if senderAvatar(model.KindSaved, user) != 77 {
		t.Fatal("a user's forward has no avatar in Saved Messages")
	}
	hidden := model.Message{ForwardDate: time.Unix(10, 0), ForwardName: "Hidden"}
	if senderAvatar(model.KindSaved, hidden) != 0 {
		t.Fatal("a hidden sender's forward has an avatar")
	}
	p := &chatPage{kind: model.KindSaved, openSender: func(model.Message) {}}
	if !p.senderOpens(user) || p.senderOpens(hidden) {
		t.Fatalf("opens: user %v, hidden %v", p.senderOpens(user), p.senderOpens(hidden))
	}
	if got := p.avatarSender(user); got.SenderID != 77 || got.SenderName != "Pavel" {
		t.Fatalf("the avatar opens %+v", got)
	}
	if p.avatarName(user) != "Pavel" {
		t.Fatalf("initials of %q", p.avatarName(user))
	}
}

// In Saved Messages forwards from different people in a row are groups of
// their own, each with its avatar; elsewhere a sender's messages are one
// group whatever they forward.
func TestSavedForwardsGroupBySource(t *testing.T) {
	at := time.Unix(1000, 0)
	from := func(id int64, name string) model.Message {
		return model.Message{Date: at, ForwardDate: at, ForwardFromID: id, ForwardName: name}
	}
	msgs := []model.Message{from(77, "Pavel"), from(78, "Anna"), from(78, "Anna")}
	if got := messageJoins(msgs, true); got[0]&joinBelow != 0 || got[1]&joinBelow == 0 {
		t.Fatalf("Saved Messages joins %v", got)
	}
	if got := messageJoins(msgs, false); got[0]&joinBelow == 0 {
		t.Fatalf("a chat's joins %v", got)
	}
}

// A message forwarded from a channel's post opens the channel at it; one
// from a user, or a post of another chat, opens it where it would.
func TestSenderPost(t *testing.T) {
	post := model.Message{ForwardFromID: -1000000000042, ForwardPostID: 15}
	channel := model.Chat{ID: -1000000000042, Kind: model.KindChannel}
	if senderPost(post, channel) != 15 {
		t.Fatal("a forwarded post does not open the channel at it")
	}
	if senderPost(post, model.Chat{ID: -1000000000043, Kind: model.KindChannel}) != 0 {
		t.Fatal("a post opens another channel at it")
	}
	if senderPost(model.Message{ForwardFromID: 77}, model.Chat{ID: 77, Kind: model.KindUser}) != 0 {
		t.Fatal("a user's forward opens at a post")
	}
}
