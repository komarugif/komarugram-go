package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// predictRows are rows of every kind the profile tells at another width:
// texts short and long, sent and received, a reply, reactions, a forward,
// photos and a GIF with a caption and without, a post with its comments
// bar, quotes and code.
func predictRows(first model.MessageID) []model.Message {
	long := strings.Repeat("Telegram keeps every message of the chat in its history. Сообщения переносятся по словам. ", 4)
	reactions := []model.Reaction{{Emoji: "👍", Count: 3}, {Emoji: "🔥", Count: 12}, {Emoji: "😁", Count: 1}, {Emoji: "🎉", Count: 140}, {Emoji: "❤", Count: 7}}
	photo := func(w, h int) *model.MessageMedia {
		return &model.MessageMedia{ID: "photo/p", MIMEType: "image/jpeg", Width: w, Height: h}
	}
	var msgs []model.Message
	add := func(m model.Message) {
		m.Key = model.MessageKey{ChatID: 1, MessageID: first + model.MessageID(len(msgs))}
		m.ContentRevision = uint64(m.Key.MessageID)
		if m.Kind == 0 && m.Media == nil {
			m.Kind = model.MessageText
		}
		if !m.Outgoing && m.SenderName == "" {
			m.SenderName, m.SenderID = "Ann", 7
		}
		msgs = append(msgs, m)
	}
	add(model.Message{Text: "Hello"})
	add(model.Message{Text: long})
	add(model.Message{Text: long, Outgoing: true})
	add(model.Message{Text: "A short answer", ReplyToMessageID: 3})
	add(model.Message{Text: long, ReplyToMessageID: 3, Outgoing: true})
	add(model.Message{Text: "Reacted to", Reactions: reactions})
	add(model.Message{Text: long, Reactions: reactions[:2]})
	add(model.Message{Text: long, ForwardDate: time.Unix(900, 0), ForwardName: "A channel with a rather long name"})
	add(model.Message{Kind: model.MessagePhoto, Media: photo(1280, 720), Text: "A caption under a photo"})
	add(model.Message{Kind: model.MessagePhoto, Media: photo(1280, 720), Text: long})
	add(model.Message{Kind: model.MessagePhoto, Media: photo(720, 1600), Text: long})
	add(model.Message{Kind: model.MessagePhoto, Media: photo(1280, 720)})
	add(model.Message{Kind: model.MessagePhoto, Media: photo(300, 200), Outgoing: true})
	add(model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif/g", MIMEType: "video/mp4", Width: 480, Height: 270}})
	add(model.Message{Text: long, Post: true, CommentsOpen: true, Comments: 5})
	add(model.Message{Text: "Short post", Post: true, CommentsOpen: true})
	add(model.Message{Text: long, ReplyToMessageID: 3, Reactions: reactions})
	add(model.Message{Kind: model.MessageFile, Media: &model.MessageMedia{ID: "doc/d", FileName: "a report with a long name.pdf", MIMEType: "application/pdf", Size: 123456}})
	add(model.Message{Kind: model.MessageFile, Media: &model.MessageMedia{ID: "doc/d", FileName: "notes.txt", MIMEType: "text/plain", Size: 2048}, Text: long})
	// Albums: pictures in rows of two, the first alone where they are odd,
	// captions under them, the time on the last tile or under them.
	album := func(caps []string, sizes ...image.Point) {
		var parts []model.Message
		for i, size := range sizes {
			part := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: first + model.MessageID(len(msgs)*10+i)}, Kind: model.MessagePhoto, GroupedID: int64(len(msgs) + 1), SenderName: "Ann", SenderID: 7,
				Media: &model.MessageMedia{ID: "photo/a", MIMEType: "image/jpeg", Width: size.X, Height: size.Y}, ContentRevision: uint64(i + 1)}
			if i < len(caps) {
				part.Text = caps[i]
			}
			parts = append(parts, part)
		}
		grouped := model.GroupAlbums(parts)[0]
		grouped.Key.MessageID = first + model.MessageID(len(msgs))
		msgs = append(msgs, grouped)
	}
	album(nil, image.Pt(1280, 720), image.Pt(720, 1280))
	album(nil, image.Pt(1280, 720), image.Pt(800, 800), image.Pt(2000, 1000))
	album([]string{long}, image.Pt(1280, 720), image.Pt(720, 1280), image.Pt(1000, 1000), image.Pt(600, 900))
	album([]string{"First", "", "Third, " + long}, image.Pt(1280, 720), image.Pt(720, 1280), image.Pt(1000, 1000))
	for _, m := range profileTexts()[4:8] {
		m.Key = model.MessageKey{}
		add(m)
	}
	return msgs
}

// A row's profile, taken at one width, tells how high the row is at any
// other, and how wide its bubble: with the avatar of the sender beside it
// and without.
func TestProfilePredictsTheRow(t *testing.T) {
	for _, avatars := range []bool{false, true} {
		t.Run(map[bool]string{false: "alone", true: "avatars"}[avatars], func(t *testing.T) {
			predictTheRows(t, avatars)
		})
	}
}

func predictTheRows(t *testing.T, avatars bool) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[2].Text = strings.Repeat("The message replied to, long enough for its quote to want two lines. ", 3)
	})
	h.page.openComments = func(model.Message) {}
	if avatars {
		h.page.avatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(gtx.Dp(size), gtx.Dp(size))}
		}
		h.frame()
	}
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	lay := func(m model.Message, width int) (*messageRow, int) {
		gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(width, 1<<20)}, Metric: metric, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		dims := h.page.row(gtx, m, false, 0, localization.For("en"), false)
		return h.page.rows[m.Key.MessageID], dims.Size.Y
	}
	for _, m := range predictRows(2001) {
		// Laid out until its media tell their state, which may change its
		// layout, as the history does: the profile is taken anew then
		// (retakeProfile).
		_, height := lay(m, 600)
		for range 10 {
			time.Sleep(2 * time.Millisecond)
			if _, again := lay(m, 600); again != height {
				height = again
			}
		}
		h.page.retakeProfile(m.Key.MessageID)
		r, height := lay(m, 600)
		if avatars && !m.Outgoing && m.Kind != model.MessageFile && r.capture.slot == 0 {
			t.Fatalf("message %d has no avatar beside it", m.Key.MessageID)
		}
		data := h.page.takeProfile(r, m, 600, height)
		if m.Kind == model.MessageFile {
			// Its name is a label the profile does not wrap.
			if data != nil {
				t.Fatalf("message %d, a file, took a profile", m.Key.MessageID)
			}
			continue
		}
		if data == nil {
			t.Fatalf("message %d took no profile", m.Key.MessageID)
		}
		prof, ok, err := decodeRowProfile(data)
		if !ok || err != nil {
			t.Fatalf("message %d: the profile reads as %v, %v", m.Key.MessageID, ok, err)
		}
		// A chip wider than the room the bubble has is cut, as wide as the
		// room lets it be, which the profile does not tell: no window is so
		// narrow, the page being at least pageMinWidth wide, but a font
		// whose emoji and digits are wide enough makes the sweep reach it.
		chipRoom := 0
		for _, c := range prof.chips {
			chipRoom = max(chipRoom, c)
		}
		chipRoom += prof.bubble.slot + int(2*bubblePadSide+rowInsetLeft+rowInsetRight)
		for _, width := range append(widthsFrom(150, 696, 17), 600) {
			if width < chipRoom {
				continue
			}
			r, want := lay(m, width)
			wantWidth := r.bodySize.X - prof.bubble.slot
			got, gotWidth, exact := prof.predict(metric, width)
			// A line cut short is as wide as the glyphs it is cut at: the
			// width told is the most it is.
			if got != want || gotWidth != wantWidth && (exact || gotWidth < wantWidth || gotWidth-wantWidth > 12) {
				t.Errorf("message %d at %d px: the profile tells %d px high, %d wide; laid out %d, %d", m.Key.MessageID, width, got, gotWidth, want, wantWidth)
			}
		}
	}
}

// widthsFrom are the widths from first to last, step apart.
func widthsFrom(first, last, step int) []int {
	var ws []int
	for w := first; w <= last; w += step {
		ws = append(ws, w)
	}
	return ws
}
