// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// Typing goes first, counted; then another action; then a game. What is
// over, or of another thread, does not count.
func TestActivity(t *testing.T) {
	l := For("en")
	now := time.Unix(1000, 0)
	act := func(name string, a model.ChatAction) model.Activity {
		return model.Activity{Name: name, Action: a, Until: now.Add(time.Second)}
	}
	over := act("Gone", model.ActionTyping)
	over.Until = now
	thread := act("Elsewhere", model.ActionTyping)
	thread.Top = 7
	for _, c := range []struct {
		acts    []model.Activity
		private bool
		want    string
		action  model.ChatAction
	}{
		{[]model.Activity{act("Ann", model.ActionTyping)}, true, "typing", model.ActionTyping},
		{[]model.Activity{act("Ann", model.ActionTyping)}, false, "Ann is typing", model.ActionTyping},
		{[]model.Activity{act("Ann", model.ActionUploadPhoto), act("Bob", model.ActionTyping), act("Cid", model.ActionTyping)}, false, "Bob and Cid are typing", model.ActionTyping},
		{[]model.Activity{act("A", model.ActionTyping), act("B", model.ActionTyping), act("C", model.ActionTyping)}, false, "3 people are typing", model.ActionTyping},
		{[]model.Activity{act("Ann", model.ActionPlayGame), act("Bob", model.ActionRecordVoice)}, false, "Bob is recording a voice message", model.ActionRecordVoice},
		{[]model.Activity{act("Ann", model.ActionChooseContact)}, true, "typing", model.ActionChooseContact},
		{[]model.Activity{act("Ann", model.ActionPlayGame)}, false, "Ann is playing a game", model.ActionPlayGame},
		{[]model.Activity{act("Ann", model.ActionChooseSticker)}, true, "choosing a sticker", model.ActionChooseSticker},
		{[]model.Activity{over, thread}, false, "", model.ActionNone},
	} {
		text, action := l.Activity(c.acts, 0, c.private, now)
		if text != c.want || action != c.action {
			t.Errorf("%+v: %q %v, want %q %v", c.acts, text, action, c.want, c.action)
		}
	}
	if text, _ := l.Activity([]model.Activity{thread}, -1, false, now); text != "Elsewhere is typing" {
		t.Errorf("any thread: %q", text)
	}
	if text, _ := For("ru").Activity([]model.Activity{act("А", model.ActionTyping), act("Б", model.ActionTyping), act("В", model.ActionTyping), act("Г", model.ActionTyping), act("Д", model.ActionTyping)}, 0, false, now); text != "5 человек печатают" {
		t.Errorf("ru: %q", text)
	}
}
