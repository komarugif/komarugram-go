// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"komarugram/internal/messenger/model"
	"testing"
)

func TestSafeURLs(t *testing.T) {
	for _, url := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://user:password@example.org", "http://", "tg://resolve?domain=a"} {
		if _, ok := safeURL(url); ok {
			t.Errorf("unsafe URL accepted %q", url)
		}
	}
	for _, url := range []string{"https://telegram.org", "telegram.org/path", "http://localhost:8000/path"} {
		if _, ok := safeURL(url); !ok {
			t.Errorf("valid URL rejected %q", url)
		}
	}
}
func TestRestoreDeletedAnchorUsesNearestPredecessor(t *testing.T) {
	p := chatPage{messages: []model.Message{{Key: model.MessageKey{MessageID: 10}}, {Key: model.MessageKey{MessageID: 20}}, {Key: model.MessageKey{MessageID: 40}}}}
	p.restore(30, 15)
	if p.list.Position.First != 1 || p.list.Position.Offset != 15 {
		t.Fatal(p.list.Position)
	}
}

func TestMediaGeometryAndAvatarScope(t *testing.T) {
	for _, v := range [][4]int{{1600, 900, 420, 360}, {800, 1600, 420, 360}, {640, 40, 420, 360}, {0, 0, 420, 360}} {
		size := fitMediaSize(v[0], v[1], v[2], v[3])
		if size.X <= 0 || size.Y <= 0 || size.X > v[2] || size.Y > v[3] {
			t.Fatal(size)
		}
		if v[0] > 0 && v[1] > 0 {
			delta := size.X*v[1] - size.Y*v[0]
			if delta < 0 {
				delta = -delta
			}
			if delta > max(v[0], v[1]) {
				t.Fatal("distorted placeholder", v, size)
			}
		}
	}
	m := model.Message{SenderID: 17, ForwardFromID: -1000000000042}
	for _, kind := range []model.ChatKind{model.KindUser, model.KindBot} {
		if senderAvatar(kind, m) != 0 {
			t.Fatal("private message avatar")
		}
	}
	if senderAvatar(model.KindGroup, m) != 17 || senderAvatar(model.KindSaved, m) != m.ForwardFromID {
		t.Fatal("missing sender/channel")
	}
	// A user's forward has their avatar too, as Telegram Desktop's.
	m.ForwardFromID = 17
	if senderAvatar(model.KindSaved, m) != 17 {
		t.Fatal("saved user forward has no avatar")
	}
}
func TestStickyAvatarStaysWithinMessage(t *testing.T) {
	for _, v := range [][6]int{{0, 1200, 600, 34, 4, 562}, {-500, 700, 600, 34, 4, 562}, {-600, 80, 600, 34, 4, 46}, {-600, 20, 600, 34, 4, -14}, {580, 1200, 600, 34, 4, 580}, {100, 200, 600, 34, 4, 166}} {
		if got := stickyAvatarY(v[0], v[1], v[2], v[3], v[4]); got != v[5] {
			t.Fatal(v, got)
		}
	}
}
