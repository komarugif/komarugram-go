// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"strings"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gioui.org/layout"
)

// someReactions are n reactions, each of its own emoji.
func someReactions(n int) []model.Reaction {
	emoji := []string{"❤", "👍", "🔥", "😁", "🎉", "🤔", "👏", "😢", "🙏", "👌", "😱", "🤯"}
	var rs []model.Reaction
	for i := range n {
		rs = append(rs, model.Reaction{Emoji: emoji[i%len(emoji)], Count: 3 + i*7})
	}
	return rs
}

// The time goes on the last row of the reactions when it fits there, as on
// a text's last line: a short message with a reaction is only the row of
// chips taller than one without.
func TestTimeOnReactionsRow(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[1].Text, m[2].Text = "a", "a"
		m[2].Reactions = someReactions(2)
	})
	h.frames(2)
	plain, reacted := h.page.rows[2], h.page.rows[3]
	if plain == nil || reacted == nil {
		t.Fatal("rows not laid out")
	}
	row := reacted.lastChips
	if row.Empty() {
		t.Fatal("no row of chips")
	}
	if got, want := reacted.bodySize.Y-plain.bodySize.Y, 8+row.Dy(); got != want {
		t.Fatalf("the reactions add %d px, want %d: the gap and the row with the time on it", got, want)
	}
}

// Reactions that would make a short message's bubble much wider than its
// text wrap onto a row more, about even; the time stays on the last.
func TestReactionsWrapToNarrowBubble(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[2].Text = "a"
		m[2].Reactions = someReactions(6)
	})
	h.frames(2)
	r := h.page.rows[3]
	if r == nil || len(r.chips) != 6 {
		t.Fatal("chips not laid out")
	}
	one := 0
	for _, c := range r.chips {
		one += c.size.X + 6
	}
	if r.lastChips.Min.Y == 0 {
		t.Fatalf("the chips stay on one row, %d px wide", one)
	}
	if r.lastChips.Min.Y > r.chips[0].size.Y+6 {
		t.Fatalf("the chips take more than two rows: the last at %d", r.lastChips.Min.Y)
	}
	if r.bodySize.X > one*3/4 {
		t.Fatalf("the bubble is %d px wide, of %d for the chips on one row", r.bodySize.X, one)
	}
}

// The reactions of a long text keep to the text's width, on as few rows as
// they take there.
func TestReactionsUnderLongText(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[1].Text = strings.Repeat("A long message that fills the bubble. ", 6)
		m[2].Text = m[1].Text
		m[2].Reactions = someReactions(2)
	})
	h.frames(2)
	plain, reacted := h.page.rows[2], h.page.rows[3]
	if plain == nil || reacted == nil {
		t.Fatal("rows not laid out")
	}
	if reacted.bodySize.X != plain.bodySize.X {
		t.Fatalf("a text with reactions is %d px wide, %d without", reacted.bodySize.X, plain.bodySize.X)
	}
	if reacted.lastChips.Min.Y != 0 {
		t.Fatal("two reactions wrap under a long text")
	}
}

// A quote of a long message replied to takes two lines rather than make a
// short reply as wide as the history; one that is short keeps to one.
func TestReplyQuoteWraps(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[0].Text = strings.Repeat("What was said at length before. ", 8)
		m[1].Text = "Short"
		m[2].Text, m[2].ReplyToMessageID = "a", 1
		m[3].Text, m[3].ReplyToMessageID = "a", 2
		m[4].Text = strings.Repeat("A long message that fills the bubble. ", 6)
	})
	h.frames(2)
	long, short, full := h.page.rows[3], h.page.rows[4], h.page.rows[5]
	if long == nil || short == nil || full == nil {
		t.Fatal("rows not laid out")
	}
	if long.bodySize.X > full.bodySize.X*2/3 {
		t.Fatalf("a reply to a long message is %d px wide, a full bubble %d", long.bodySize.X, full.bodySize.X)
	}
	// The second line of the quote is all it adds.
	if extra := long.bodySize.Y - short.bodySize.Y; extra <= 0 || extra > 24 {
		t.Fatalf("a quote on two lines adds %d px", extra)
	}
}

// postLikeChannel makes m a post as https://t.me/uxfromhell/12666 is: a
// tall video with a caption of two lines, a reply to a long post and ten
// reactions under it.
func postLikeChannel(m []model.Message, i int, reply model.MessageID) {
	m[i].Post, m[i].SenderName, m[i].Views = true, "", 6905
	m[i].Text = "Удивлён ли я, что строители этой дороги в Пакистане не задались библейским вопросом?"
	m[i].Kind, m[i].Media = model.MessageVideo, &model.MessageMedia{ID: "document/1", MIMEType: "video/mp4", Width: 478, Height: 854, Duration: 19767000000}
	m[i].ReplyToMessageID = reply
	m[i].Reactions = someReactions(10)
}

// A caption under media narrower than it wraps as narrow as it keeps its
// lines: two lines under a tall video took the whole width, and the bubble
// with them, half of it empty beside the video.
func TestCaptionKeepsBubbleNarrow(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		// Two lines at the whole width, the second of a word or two.
		m[1].Text = strings.TrimSpace(strings.Repeat("дорога ", 8))
		m[2].Text, m[2].Kind, m[2].Media = m[1].Text, model.MessageVideo, &model.MessageMedia{ID: "document/1", MIMEType: "video/mp4", Width: 478, Height: 854}
	})
	h.frames(2)
	alone, under := h.page.rows[2], h.page.rows[3]
	if alone == nil || under == nil {
		t.Fatal("rows not laid out")
	}
	if under.text.size.Y != alone.text.size.Y {
		t.Fatalf("the caption is %d px high under the video, %d alone", under.text.size.Y, alone.text.size.Y)
	}
	if under.text.size.X > alone.text.size.X*3/4 {
		t.Fatalf("the caption is %d px wide under the video, %d alone", under.text.size.X, alone.text.size.X)
	}
}

// TestRenderBubbleFit saves bubbles whose reactions or quote would make them
// wide, for looking at them: BUBBLE_FIT_PNG=/tmp/fit.png.
func TestRenderBubbleFit(t *testing.T) {
	path := os.Getenv("BUBBLE_FIT_PNG")
	if path == "" {
		t.Skip("set BUBBLE_FIT_PNG to a file")
	}
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		for i := range m {
			m[i].SenderID = int64(i + 1)
			m[i].SenderName = "Анна"
		}
		m[0].Text = "Кто идёт в пятницу на встречу? Нужно понять, сколько брать пиццы и хватит ли места в зале на всех, кто записался"
		m[1].Text, m[1].Reactions = "Я", someReactions(2)
		m[2].Text, m[2].Reactions = "Отлично!", someReactions(7)
		m[3].Text, m[3].ReplyToMessageID = "Я буду", 1
		m[4].Text, m[4].Reactions, m[4].Outgoing = "Иду", someReactions(4), true
		m[5].Text, m[5].Reactions = strings.Repeat("Длинный текст сообщения, который занимает несколько строк. ", 3), someReactions(5)
		m[6].Text, m[6].ReplyToMessageID, m[6].Reactions = "Да", 1, someReactions(3)
		m[7].Text, m[7].Reactions = "Хорошо", someReactions(12)
		m[8].Text = "В очередной раз удивляюсь таким вот вещам\n\nНу ок, кто-то (наверное) отправил косячный макет в типографию. Бывает. Но ведь там его кто-то напечатал."
		postLikeChannel(m, 9, 9)
	})
	p := h.page
	l := localization.For("ru")
	renderFrames(t, image.Pt(700, 2400), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}
