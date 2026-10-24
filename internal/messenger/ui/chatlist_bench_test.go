// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// benchChats are chats whose last messages are as a list of an account
// has them: words alone mostly, some with a sender, a photo or formatting.
func benchChats(n int) []model.Chat {
	chats := make([]model.Chat, n)
	now := time.Now()
	for i := range chats {
		c := model.Chat{ID: int64(i + 1), Kind: model.KindUser, Title: fmt.Sprintf("Chat %d", i), LastTime: now.Add(-time.Duration(i) * time.Minute),
			Last: model.Preview{ID: model.MessageID(i + 1), Text: "A last message long enough to be cut at the end of the row of the list"}}
		switch i % 4 {
		case 1:
			c.Kind, c.Last.Sender = model.KindGroup, "Anna"
		case 2:
			c.Last.Media = "preview.photo"
			c.Last.Entities = []model.Entity{{Kind: "italic", Offset: 2, Length: 4}, {Kind: "spoiler", Offset: 12, Length: 6}}
		case 3:
			c.Unread = i
		}
		chats[i] = c
	}
	return chats
}

func BenchmarkChatListFrame(b *testing.B) {
	l := newChatList()
	var images imageOps
	chats := benchChats(40)
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Now: time.Now(), Constraints: layout.Exact(image.Pt(360, 900)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: make(map[string]any)}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	catalog := localization.For(string(localization.English))
	render := func() {
		ops.Reset()
		images.BeginFrame()
		l.Layout(gtx, section{kind: sectionAll}, nil, chats, 0, false, catalog)
		images.EndFrame()
	}
	render()
	render()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		render()
	}
}
