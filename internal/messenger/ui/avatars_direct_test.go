// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"

	"komarugram/internal/messenger/model"
)

// TestNoAvatarsInDirectMessages leaves the avatars out of a channel's
// direct messages, a group of Telegram's read as a private chat, and keeps
// them in a group.
func TestNoAvatarsInDirectMessages(t *testing.T) {
	m := model.Message{Key: model.MessageKey{ChatID: -1000040}, SenderID: -1000004, Kind: model.MessageText}
	group := &chatPage{kind: model.KindGroup}
	if group.senderAvatar(m) == 0 {
		t.Fatal("a group's message has no avatar")
	}
	direct := &chatPage{kind: model.KindGroup, direct: true}
	if id := direct.senderAvatar(m); id != 0 {
		t.Fatalf("a direct message has the avatar of %d", id)
	}
}
