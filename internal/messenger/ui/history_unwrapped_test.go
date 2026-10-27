// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

func TestLoneEmoji(t *testing.T) {
	text := func(s string) model.Message { return model.Message{Kind: model.MessageText, Text: s} }
	custom := text("🙂")
	custom.Entities = []model.Entity{{Kind: "emoji", Offset: 0, Length: 2, DocumentID: 7}}
	bold := text("👍")
	bold.Entities = []model.Entity{{Kind: "bold", Offset: 0, Length: 2}}
	for _, c := range []struct {
		m   model.Message
		ok  bool
		doc int64
	}{
		{text("👍"), true, 0},
		{text(" 🔥\n"), true, 0},
		{text("❤"), true, 0},     // without U+FE0F, as Telegram sends it
		{text("❤️"), true, 0},    // with it
		{text("👨‍👩‍👧"), true, 0}, // a family: one grapheme of five code points
		{text("🇷🇺"), true, 0},    // a flag
		{text("1️⃣"), true, 0},   // a keycap
		{text("👍🏽"), true, 0},    // with a skin tone
		{custom, true, 7},
		{text("😀😀"), false, 0},
		{text("а"), false, 0},
		{text("1"), false, 0},
		{text("©"), false, 0},
		{text("👍 ok"), false, 0},
		{bold, false, 0},
		{model.Message{Kind: model.MessagePhoto, Text: "👍", Media: &model.MessageMedia{}}, false, 0},
	} {
		_, doc, ok := loneEmoji(c.m)
		if ok != c.ok || doc != c.doc {
			t.Errorf("%q: got %t, %d; want %t, %d", c.m.Text, ok, doc, c.ok, c.doc)
		}
	}
}

// TestUnwrappedHeight checks that a lone emoji takes half a sticker, with
// no bubble around it, and two emoji stay in one.
func TestUnwrappedHeight(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[5].Text = "👍"
		messages[6].Text = "👍👍"
	})
	h.frames(3)
	p := h.page
	height := func(id model.MessageID) int {
		for i, m := range p.messages {
			if m.Key.MessageID == id {
				return int(p.heights.Prefix(i+1) - p.heights.Prefix(i))
			}
		}
		t.Fatalf("message %d not shown", id)
		return 0
	}
	// The harness draws at 1 px per dp; rows keep a margin of a few dp.
	if got := height(6); got < int(largeEmojiSize) || got > int(largeEmojiSize)+12 {
		t.Errorf("a lone emoji row is %d high, want about %d", got, int(largeEmojiSize))
	}
	if !p.unwrapped(p.messages[5]) || p.unwrapped(p.messages[6]) {
		t.Error("the lone emoji has a bubble, or two emoji lost theirs")
	}
}

// TestRenderUnwrapped saves stickers and lone emoji over the chat's
// background, for looking at them: UNWRAPPED_PNG=/tmp/unwrapped.png.
func TestRenderUnwrapped(t *testing.T) {
	path := os.Getenv("UNWRAPPED_PNG")
	if path == "" {
		t.Skip("set UNWRAPPED_PNG to a file")
	}
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		for i, e := range []string{"👍", "❤", "🇷🇺", "👨‍👩‍👧", "😀😀"} {
			m := &messages[14+i]
			m.Text, m.Outgoing = e, i%2 == 1
			if i == 2 {
				m.ReplyToMessageID = 13
			}
		}
		messages[19].Reactions = []model.Reaction{{Emoji: "❤", Count: 2}}
		// A custom emoji under a spoiler: one band, as high as the line.
		messages[13].Text = "Ответ: это был 🔥 дворецкий"
		messages[13].Entities = []model.Entity{{Kind: "spoiler", Offset: 7, Length: 20}, {Kind: "emoji", Offset: 15, Length: 2, DocumentID: 7}}
	})
	p := h.page
	p.list.Position.BeforeEnd = false
	l := localization.For("ru")
	renderFrames(t, image.Pt(500, 900), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}
