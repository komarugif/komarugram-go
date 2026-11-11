// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"strings"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
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
	// The reactions keep to the text's width: the bubble is no wider for them.
	// Without them it may be a little wider than its text, the time going
	// beside the last line, where the font lets it.
	if reacted.bodySize.X > plain.bodySize.X {
		t.Fatalf("a text with reactions is %d px wide, %d without", reacted.bodySize.X, plain.bodySize.X)
	}
	if reacted.bodySize.X < plain.text.size.X {
		t.Fatalf("a text with reactions is %d px wide, its text %d", reacted.bodySize.X, plain.text.size.X)
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
	// Two lines at the whole width, the second of a word or two: as many
	// words as the font takes to go onto a second line.
	oneLine := 0
	for words := 1; words <= 40; words++ {
		text := strings.TrimSpace(strings.Repeat("дорога ", words))
		h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
			m[1].Text = text
			m[2].Text, m[2].Kind, m[2].Media = text, model.MessageVideo, &model.MessageMedia{ID: "document/1", MIMEType: "video/mp4", Width: 478, Height: 854}
		})
		h.frames(2)
		alone, under := h.page.rows[2], h.page.rows[3]
		if alone == nil || under == nil {
			t.Fatal("rows not laid out")
		}
		if oneLine == 0 {
			oneLine = alone.text.size.Y
		}
		if alone.text.size.Y < oneLine*3/2 {
			continue
		}
		if under.text.size.Y != alone.text.size.Y {
			t.Fatalf("the caption of %d words is %d px high under the video, %d alone", words, under.text.size.Y, alone.text.size.Y)
		}
		if under.text.size.X > alone.text.size.X*3/4 {
			t.Fatalf("the caption of %d words is %d px wide under the video, %d alone", words, under.text.size.X, alone.text.size.X)
		}
		return
	}
	t.Fatal("no caption took two lines")
}

// signedPost makes m a post as https://t.me/komaru_GIF/17023 is: a photo
// with a short caption, four reactions, and its views and its author's
// signature in its line of time, which beside the chips fits the history
// but not the photo.
func signedPost(m *model.Message) {
	m.Post, m.SenderName, m.Views, m.PostAuthor = true, "", 547, "·°⌛ квека .°"
	m.Text, m.Kind = "Komaru GIF / Чат / VPN", model.MessagePhoto
	m.Media = &model.MessageMedia{ID: "photo/9", MIMEType: "image/jpeg", Width: 960, Height: 1280}
	m.Reactions = someReactions(4)
}

// The time goes on a line of its own where beside the reactions it would
// make the bubble much wider than the rest of what it holds: a post's
// author's long signature beside four reactions under a photo made the
// bubble as wide as the history, half of it empty beside the photo.
func TestLongSignatureUnderReactions(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		signedPost(&m[1])
	})
	h.frames(2)
	r := h.page.rows[2]
	if r == nil || len(r.chips) != 4 {
		t.Fatal("chips not laid out")
	}
	gtx := layout.Context{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}, Ops: new(op.Ops), Constraints: layout.Constraints{Max: image.Pt(1000, 100)}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	foot := messageFooter(gtx, r.shown, localization.For("en"), true, model.SendNone).Size.X
	row := r.lastChips.Max.X
	// The bubble is no wider than the chips or the time alone and its
	// padding, or the photo, 322 px of 960 by 1280 at most 430 high, and
	// its 3 px each side: not as wide as the chips and the time side by
	// side.
	if want := max(row+24, foot+24, 322+6); r.bodySize.X > want {
		t.Fatalf("the bubble is %d px wide, want at most %d: the time is beside the chips (%d) though it is %d px wide", r.bodySize.X, want, row, foot)
	}
}

// spamPost makes m a post as https://t.me/uxfromhell/12671 is: a photo with
// a caption of two lines, eight reactions, its views and the comments bar.
func spamPost(m *model.Message) {
	m.Post, m.SenderName, m.Views, m.Comments, m.CommentsOpen = true, "", 6698, 2, true
	m.Text = "Отписался от нашего спама? Ну и пидор же ты! Мы тогда удаляем твой аккаунт. У тебя есть месяц, чтобы одуматься"
	m.Kind = model.MessagePhoto
	m.Media = &model.MessageMedia{ID: "photo/12671", MIMEType: "image/jpeg", Width: 643, Height: 505}
	// Its counts, the custom emoji of the first and the third as emoji.
	m.Reactions = nil
	for i, n := range []int{275, 76, 39, 5, 4, 3, 2, 2} {
		m.Reactions = append(m.Reactions, model.Reaction{Emoji: []string{"😳", "🖕", "🗿", "❤", "😱", "👌", "⚡", "🤯"}[i], Count: n})
	}
}

// Reactions with the time beside them wider than the rest of the bubble
// wrap onto even rows, the time on the last, where that keeps the bubble
// to the rest: eight reactions and a post's views and time on one row
// made it a fifth wider than its photo and caption.
func TestReactionsWrapWithTime(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		spamPost(&m[1])
	})
	h.width = 700
	h.frames(3)
	r := h.page.rows[2]
	if r == nil || len(r.chips) != 8 {
		t.Fatal("chips not laid out")
	}
	if r.lastChips.Min.Y == 0 {
		t.Fatalf("the chips stay on one row, the bubble %d px wide", r.bodySize.X)
	}
	if r.lastChips.Min.Y > r.chips[0].size.Y+6 {
		t.Fatalf("the chips take more than two rows: the last %v", r.lastChips)
	}
	// The bubble is as wide as the caption and the photo, not the chips.
	if text := r.text.size.X + 24; r.bodySize.X > max(text, 420+6) {
		t.Fatalf("the bubble is %d px wide, its caption %d", r.bodySize.X, text)
	}
}

// komaruPost makes m a post as Komaru GIF's are: an upright video with a
// short caption, five reactions, and its views and author's signature in
// its line of time.
func komaruPost(m *model.Message) {
	m.Post, m.SenderName, m.Views, m.PostAuthor = true, "", 1900, "Kotik Admin"
	m.Text, m.Kind = "Komaru GIF / Чат / VPN", model.MessageVideo
	m.Media = &model.MessageMedia{ID: "document/17", MIMEType: "video/mp4", Width: 478, Height: 854, Duration: 67e9}
	m.Reactions = nil
	// Counts of thousands, as "6.3K": their chips are wider than the video
	// with the emoji and the digits of any font. Counts of hundreds made
	// chips of 60 px in a narrower font, and two rows saved the bubble less
	// than the 64 px the reactions are worth wrapping for.
	for i, n := range []int{6300, 1500, 1300, 2000, 1000} {
		m.Reactions = append(m.Reactions, model.Reaction{Emoji: []string{"😳", "❤", "🥰", "🔥", "🍌"}[i], Count: n})
	}
}

// Five reactions wider than an upright video and its caption wrap one or
// two onto a row of their own, the time under them: on one row they kept
// the bubble half again as wide as the video.
func TestFewReactionsWrapUnderVideo(t *testing.T) {
	// A row of chips may be this much wider than the evenest: half a chip.
	const chipSlack = 40
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		komaruPost(&m[1])
	})
	h.frames(2)
	r := h.page.rows[2]
	if r == nil || len(r.chips) != 5 {
		t.Fatal("chips not laid out")
	}
	if r.lastChips.Min.Y == 0 {
		t.Fatalf("the chips stay on one row, the bubble %d px wide", r.bodySize.X)
	}
	// The rows are about even: the widest is within chipSlack of as narrow as
	// any split of the chips into two rows makes it, whatever the widths of
	// the font's emoji and digits are, and fills a row as far as the video
	// lets it. The chips of the last row are those from the end that make its
	// width.
	rowWidth := func(chips []reactionChip) int {
		w := 0
		for j, c := range chips {
			if j > 0 {
				w += 6
			}
			w += c.size.X
		}
		return w
	}
	under, k := 0, len(r.chips)
	for k > 0 && under < r.lastChips.Max.X {
		k--
		if under > 0 {
			under += 6
		}
		under += r.chips[k].size.X
	}
	over := rowWidth(r.chips[:k])
	best := max(over, under)
	for split := 1; split < len(r.chips); split++ {
		best = min(best, max(rowWidth(r.chips[:split]), rowWidth(r.chips[split:])))
	}
	if r.lastChips.Min.Y > r.chips[0].size.Y+6 || max(over, under) > best+chipSlack {
		t.Fatalf("%d chips over %d: the widest row is %d px, %d px is possible", k, len(r.chips)-k, max(over, under), best)
	}
	// 478 by 854 at 430 high is 240 wide, 3 px each side.
	if r.bodySize.X > 246+40 {
		t.Fatalf("the bubble is %d px wide, the video 246 with its insets", r.bodySize.X)
	}
}

// quoted makes the first n letters of m's text a quote, as Telegram's
// "quote" entity does.
func quoted(m *model.Message, n int) {
	m.Entities = append(m.Entities, model.Entity{Kind: "quote", Offset: 0, Length: n})
}

// A quote is as wide as its text: it took the whole width of the history,
// and the bubble with it, a short one too.
func TestQuoteTakesItsWidth(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[1].Text = "Short quote"
		quoted(&m[1], len([]rune(m[1].Text)))
	})
	h.frames(2)
	r := h.page.rows[2]
	if r == nil {
		t.Fatal("row not laid out")
	}
	if r.text.size.X > 200 {
		t.Fatalf("a short quote is %d px wide", r.text.size.X)
	}
}

// komaruQuotePost makes m a post as https://t.me/komaru_GIF/17003 is: an
// upright video, a caption of a quote on a line and links under it.
func komaruQuotePost(m *model.Message) {
	komaruPost(m)
	quote := "Кокоа исследует комнату, в которую обычно не пускают"
	m.Text = quote + "\n\nKomaru GIF / Чат / VPN"
	m.Media = &model.MessageMedia{ID: "document/17003", MIMEType: "video/mp4", Width: 720, Height: 1280, Duration: 20e9}
	quoted(m, len([]rune(quote)))
	m.Reactions = m.Reactions[:4]
}

// A caption with a quote under an upright video wraps as one without: the
// quote on a line of its own kept the bubble as wide as the history.
func TestQuoteCaptionWraps(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		komaruQuotePost(&m[1])
	})
	h.width = 700
	h.frames(3)
	r := h.page.rows[2]
	if r == nil {
		t.Fatal("row not laid out")
	}
	// 720 by 1280 at 430 high is 241 wide, 3 px each side; the caption
	// may take a little more.
	if r.bodySize.X > 241+6+80 {
		t.Fatalf("the bubble is %d px wide, its video 247", r.bodySize.X)
	}
}

// An album's caption wraps as narrow as the album, as a single picture's
// does: two wide screenshots side by side were half the bubble, their
// caption the whole width of the history.
func TestAlbumCaptionWraps(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[1].Text = ""
		for j, size := range []image.Point{{1085, 246}, {1102, 263}} {
			part := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(200 + j)}, Kind: model.MessagePhoto,
				Media: &model.MessageMedia{ID: "photo/s", MIMEType: "image/jpeg", Width: size.X, Height: size.Y}}
			if j == 0 {
				// Between one line and two of the album's width in any font:
				// narrower, it still takes two lines.
				part.Text = "Да, мне абсолютно точно необходимо проверить кредитную историю в карточке маршрута, это именно то"
			}
			m[1].Attachments = append(m[1].Attachments, part)
		}
	})
	h.width = 700
	h.frames(3)
	r := h.page.rows[2]
	if r == nil {
		t.Fatal("row not laid out")
	}
	// The album is 500 px wide, 3 px each side.
	if r.bodySize.X > 506 {
		t.Fatalf("the bubble is %d px wide, its album 506", r.bodySize.X)
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
		signedPost(&m[10])
		spamPost(&m[11])
		komaruPost(&m[12])
		komaruQuotePost(&m[13])
		// A quote of two lines, as https://t.me/komaru_GIF/16979's.
		komaruQuotePost(&m[14])
		quote := "Играю с Кокоа\nНужно быть осторожным, а то Кокоа поцарапает."
		m[14].Text, m[14].Entities = quote+"\n\nKomaru GIF / Чат / VPN", nil
		quoted(&m[14], len([]rune(quote)))
		m[14].Media = &model.MessageMedia{ID: "document/16979", MIMEType: "video/mp4", Width: 720, Height: 960, Duration: 9e9}
		// An album of two wide screenshots, as https://t.me/uxfromhell/12660.
		m[15].Text = ""
		for j, size := range []image.Point{{1085, 246}, {1102, 263}} {
			part := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(300 + j)}, Kind: model.MessagePhoto,
				Media: &model.MessageMedia{ID: "photo/s", MIMEType: "image/jpeg", Width: size.X, Height: size.Y}}
			if j == 0 {
				part.Text = "Да, мне абсолютно точно необходимо проверить кредитную историю в карточке маршрута. Это именно то, как должна работать контекстная реклама"
			}
			m[15].Attachments = append(m[15].Attachments, part)
		}
		m[15].Reactions = someReactions(10)
	})
	p := h.page
	l := localization.For("ru")
	renderFrames(t, image.Pt(700, 6800), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}
