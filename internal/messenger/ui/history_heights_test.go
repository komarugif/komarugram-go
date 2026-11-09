package ui

import (
	"image"
	"strings"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// A history wider than rowsWidth lays its rows out as one rowsWidth wide:
// widened, it keeps its environment, and with it the heights kept.
func TestWideHistoriesKeepTheirHeights(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.width = rowsWidth(unit.Metric{PxPerDp: 1, PxPerSp: 1}) + 40
	h.frame()
	env := h.page.env
	h.width += 600
	h.frame()
	if h.page.env != env {
		t.Fatalf("widened past the rows, the environment changed: %+v, was %+v", h.page.env, env)
	}
	h.width = rowsWidth(unit.Metric{PxPerDp: 1, PxPerSp: 1}) - 40
	h.frame()
	if h.page.env == env {
		t.Fatal("narrowed under the rows, the environment stayed")
	}
}

// What rowsWidth says: no row is higher or lower in a history wider than
// it. A row laid out wider than it would make the heights kept under it
// wrong.
func TestRowsAsHighPastRowsWidth(t *testing.T) {
	h := newMenuHarness(t, nil)
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	long := strings.Repeat("A message long enough to wrap in the widest bubble there is. ", 12)
	key := func(id int) model.MessageKey { return model.MessageKey{ChatID: 1, MessageID: model.MessageID(id)} }
	msgs := []model.Message{
		{Key: key(1), Date: h.now, Text: "Short", SenderName: "Ann"},
		{Key: key(2), Date: h.now, Text: long, SenderName: "Ann"},
		{Key: key(3), Date: h.now, Text: long, Outgoing: true},
		{Key: key(4), Date: h.now, Kind: model.MessageService, Service: &model.ServiceAction{Kind: model.ServiceCreateChat, Title: strings.Repeat("A long title of a group ", 8)}},
		{Key: key(5), Date: h.now, Text: long, ReplyToMessageID: 2, SenderName: "Bob"},
		{Key: key(6), Date: h.now, Text: long, Reactions: []model.Reaction{{Emoji: "👍", Count: 3}, {Emoji: "🔥", Count: 12}, {Emoji: "😁", Count: 1}, {Emoji: "🎉", Count: 4}}},
		{Key: key(8), Date: h.now, Text: "Pick", Buttons: [][]model.MessageButton{{{Text: "One"}, {Text: "Two"}}, {{Text: strings.Repeat("A long button ", 6)}}}},
		{Key: key(9), Date: h.now, Poll: &model.Poll{Question: long, Answers: []model.PollAnswer{{Text: "Yes"}, {Text: strings.Repeat("No, not at all ", 6)}}}},
	}
	for i := range msgs {
		msgs[i].ContentRevision = uint64(i + 1)
	}
	joins := messageJoins(msgs, false)
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	heights := func(width int) []int {
		var hs []int
		for i, m := range msgs {
			gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(width, 1<<20)}, Metric: metric, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, theme)
			hs = append(hs, h.page.row(gtx, m, false, joins[i], localization.For("en"), false).Size.Y)
		}
		return hs
	}
	at := heights(rowsWidth(metric))
	for _, w := range []int{rowsWidth(metric) + 1, rowsWidth(metric) * 3} {
		for i, v := range heights(w) {
			if v != at[i] {
				t.Errorf("message %d is %d px high %d px wide, %d px at rowsWidth", msgs[i].Key.MessageID, v, w, at[i])
			}
		}
	}
}

// The height kept of the last row is the row's, without the room the
// composer takes under it: once a message comes after it, it is as high.
func TestLastRowKeptWithoutTheComposersRoom(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.page.list.Position.BeforeEnd = false
	h.frames(2)
	msgs := h.store.benchmarkHistory.h.Messages
	last := msgs[len(msgs)-1].Key.MessageID
	kept := h.page.measures[last].HeightPx
	if h.page.tail == 0 {
		t.Fatal("no room under the last row")
	}
	if total, rows := h.page.heights.Total(), h.page.heights.Prefix(len(msgs)-1); int(total-rows) != kept+h.page.tail {
		t.Fatalf("the list's last row is %d px, kept %d and the room %d", total-rows, kept, h.page.tail)
	}
	// Sent, not Ann's: a message of hers would join hers, and the space
	// between the two would be less.
	next := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: last + 1}, Date: h.now, Text: "One more", Outgoing: true, ContentRevision: 1}
	h.store.benchmarkHistory.h.Messages = append(msgs, next)
	h.store.benchmarkHistory.h.Revision++
	h.frames(2)
	if now := h.page.measures[last].HeightPx; now != kept {
		t.Fatalf("message %d was kept %d px high as the last, %d px once another came", last, kept, now)
	}
}

// The look changes the heights kept only where it changes how wide the
// time is: its seconds and its marks, not the corners of bubbles.
func TestLookRevisionTellsTheTimesWidth(t *testing.T) {
	base := preferences.Look{BubbleRadius: 8, AvatarCorners: preferences.AvatarRound}
	round := base
	round.BubbleRadius, round.AvatarCorners = 2, 0
	if lookRevision(round) != lookRevision(base) {
		t.Error("the corners changed the revision")
	}
	for _, edit := range []func(*preferences.Look){
		func(l *preferences.Look) { l.Seconds = true },
		func(l *preferences.Look) { l.EditedMark = "✎" },
		func(l *preferences.Look) { l.DeletedMark = "🗑" },
		func(l *preferences.Look) { l.EditedMark, l.DeletedMark = "", "x" },
	} {
		l := base
		edit(&l)
		if lookRevision(l) == lookRevision(base) {
			t.Errorf("%+v has the revision of %+v", l, base)
		}
	}
}

// Dark and light colors change no height: the environment is the same.
func TestThemeKeepsTheEnvironment(t *testing.T) {
	h := newMenuHarness(t, nil)
	env := h.page.env
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(500, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineDark()))
	h.page.images.BeginFrame()
	h.page.media.BeginFrame()
	h.page.Layout(gtx, model.Chat{ID: 1, Title: "Chat", Kind: model.KindGroup}, localization.For("en"), false)
	h.page.media.EndFrame()
	h.page.images.EndFrame()
	if h.page.env != env {
		t.Fatalf("dark colors changed the environment: %+v, was %+v", h.page.env, env)
	}
}
