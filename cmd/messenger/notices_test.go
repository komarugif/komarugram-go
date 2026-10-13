// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// What a notification shows follows the settings, as in Telegram Desktop;
// a muted chat, a kind of chat turned off, another account and the chat
// the focused window shows get none.
func TestNoticeFor(t *testing.T) {
	l := localization.For("en")
	all := preferences.Notify{Desktop: true, Sound: true, Name: true, Text: true, Private: true, Groups: true, Channels: true, AllAccounts: true}
	group := model.MessageNotice{Chat: model.Chat{ID: -7, Kind: model.KindGroup, Title: "Team"}, Message: model.Preview{Sender: "Alice", Text: "hello"}}
	private := model.MessageNotice{Chat: model.Chat{ID: 5, Kind: model.KindBot, Title: "Bot"}, Message: model.Preview{Text: "hi"}}
	channel := model.MessageNotice{Chat: model.Chat{ID: -9, Kind: model.KindChannel, Title: "News"}, Message: model.Preview{Text: "post"}}
	with := func(f func(*preferences.Notify)) preferences.Global {
		g := preferences.Global{Notify: all, LastAccountID: "a"}
		f(&g.Notify)
		return g
	}
	same := func(*preferences.Notify) {}
	for _, c := range []struct {
		name        string
		g           preferences.Global
		account     string
		view        noticeView
		n           model.MessageNotice
		title, body string
		sound       bool
	}{
		{"group", with(same), "a", noticeView{}, group, "Team", "Alice: hello", true},
		{"private", with(same), "b", noticeView{chat: 5}, private, "Bot", "hi", true},
		{"other chat focused", with(same), "a", noticeView{chat: 5, focused: true}, group, "Team", "Alice: hello", true},
		{"no name", with(func(n *preferences.Notify) { n.Name = false }), "a", noticeView{}, group, l.T("app.title"), "You have a new message", true},
		{"no text", with(func(n *preferences.Notify) { n.Text = false }), "a", noticeView{}, group, "Team", "You have a new message", true},
		{"locked", with(same), "a", noticeView{locked: true}, group, l.T("app.title"), "You have a new message", true},
		{"no sound", with(func(n *preferences.Notify) { n.Sound = false }), "a", noticeView{}, private, "Bot", "hi", false},
		{"silent", with(same), "a", noticeView{}, model.MessageNotice{Chat: private.Chat, Message: model.Preview{Text: "hi"}, Silent: true}, "Bot", "hi", false},
		{"this account only", with(func(n *preferences.Notify) { n.AllAccounts = false }), "a", noticeView{}, channel, "News", "post", true},
	} {
		note, ok := noticeFor(c.g, c.account, c.view, c.n, l)
		if !ok || note.Title != c.title || note.Body != c.body || note.Sound != c.sound {
			t.Errorf("%s: %v %+v", c.name, ok, note)
		}
	}
	muted := group
	muted.Chat.Muted = true
	for _, c := range []struct {
		name    string
		g       preferences.Global
		account string
		view    noticeView
		n       model.MessageNotice
	}{
		{"off", with(func(n *preferences.Notify) { n.Desktop = false }), "a", noticeView{}, group},
		{"muted", with(same), "a", noticeView{}, muted},
		{"groups off", with(func(n *preferences.Notify) { n.Groups = false }), "a", noticeView{}, group},
		{"channels off", with(func(n *preferences.Notify) { n.Channels = false }), "a", noticeView{}, channel},
		{"private off", with(func(n *preferences.Notify) { n.Private = false }), "a", noticeView{}, private},
		{"other account", with(func(n *preferences.Notify) { n.AllAccounts = false }), "b", noticeView{}, group},
		{"shown", with(same), "a", noticeView{chat: -7, focused: true}, group},
	} {
		if note, ok := noticeFor(c.g, c.account, c.view, c.n, l); ok {
			t.Errorf("%s: %+v", c.name, note)
		}
	}
	a, _ := noticeFor(with(same), "a", noticeView{}, group, l)
	b, _ := noticeFor(with(same), "b", noticeView{}, group, l)
	if a.Tag == b.Tag || a.Tag == "" {
		t.Error("tags", a.Tag, b.Tag)
	}
}
