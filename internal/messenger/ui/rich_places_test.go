// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// editsKept is the menu's store that kept an edit of each message: its
// text in italic, with a custom emoji.
type editsKept struct {
	*menuStore
}

func (s *editsKept) SetKeep(model.Keep) {}
func (s *editsKept) MessageEdits(_ context.Context, m model.Message) ([]model.Message, error) {
	old := m
	old.Text, old.EditedAt = "Draft ☀", time.Time{}
	old.Entities = []model.Entity{{Kind: model.EntityItalic, Offset: 0, Length: 5}, {Kind: model.EntityEmoji, Offset: 6, Length: 1, DocumentID: 9}}
	return []model.Message{old}, nil
}

func hasRun(runs []model.TextRun, is func(model.TextRun) bool) bool {
	for _, r := range runs {
		if is(r) {
			return true
		}
	}
	return false
}

// The edits history draws each version's text as the history draws a
// message's: with its formatting and custom emoji.
func TestEditsDrawVersionsAsTheHistory(t *testing.T) {
	h := newMenuHarnessOn(t, func(_ *menuStore, m []model.Message) {
		m[2].EditedAt = m[2].Date.Add(time.Minute)
		m[2].Entities = []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 7}}
	}, func(s *menuStore) model.ConversationStore { return &editsKept{menuStore: s} })
	var msg model.Message
	for _, m := range h.page.messages {
		if m.Key.MessageID == 3 {
			msg = m
		}
	}
	h.page.edits.open(h.page, msg)
	deadline := time.Now().Add(2 * time.Second)
	for !h.page.edits.loaded && time.Now().Before(deadline) {
		h.frame()
		time.Sleep(time.Millisecond)
	}
	h.frames(30)
	rows := h.page.edits.rows
	if len(rows) != 2 {
		t.Fatalf("%d rows for an edit and the message", len(rows))
	}
	if !hasRun(rows[0].runs, func(r model.TextRun) bool { return r.Italic }) || !hasRun(rows[0].runs, func(r model.TextRun) bool { return r.Emoji == 9 }) {
		t.Errorf("the edit lost its formatting: %+v", rows[0].runs)
	}
	if !hasRun(rows[1].runs, func(r model.TextRun) bool { return r.Bold }) {
		t.Errorf("the message lost its formatting: %+v", rows[1].runs)
	}
	for i, r := range rows {
		if len(r.text.fragments) == 0 {
			t.Errorf("version %d was not drawn as a message's text", i)
		}
	}
}

// The pinned bar shows the message's custom emoji, after what its media is,
// as a reply's quote does.
func TestPinnedRunsKeepCustomEmoji(t *testing.T) {
	h := newMenuHarness(t, nil)
	l := localization.For("en")
	m := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "p"}, Text: "Look ☀", SenderName: "Ann", Entities: []model.Entity{{Kind: model.EntityEmoji, Offset: 5, Length: 1, DocumentID: 4}}}
	runs := h.page.pinnedRuns(m, l)
	if !hasRun(runs, func(r model.TextRun) bool { return r.Emoji == 4 }) {
		t.Fatalf("no custom emoji in %+v", runs)
	}
	if runs[0].Text != "Photo, " {
		t.Fatalf("the bar starts with %q, not what the media is", runs[0].Text)
	}
}

// The viewer's caption keeps the text's formatting and custom emoji, and
// leaves the spaces around it.
func TestCaptionRunsKeepFormatting(t *testing.T) {
	l := localization.For("en")
	m := model.Message{Text: "  Bold and ☀ hidden \n", Entities: []model.Entity{
		{Kind: model.EntityBold, Offset: 2, Length: 4},
		{Kind: model.EntityEmoji, Offset: 11, Length: 1, DocumentID: 7},
		{Kind: model.EntitySpoiler, Offset: 13, Length: 6},
	}}
	runs := captionRuns(m, l, time.Now())
	if len(runs) == 0 || runs[0].Text != "Bold" || !runs[0].Bold {
		t.Fatalf("the caption starts with %+v", runs)
	}
	if !hasRun(runs, func(r model.TextRun) bool { return r.Emoji == 7 }) || !hasRun(runs, func(r model.TextRun) bool { return r.Spoiler && r.Text == "hidden" }) {
		t.Fatalf("the caption lost its emoji or spoiler: %+v", runs)
	}
	if last := runs[len(runs)-1]; last.Text != "hidden" {
		t.Fatalf("the caption ends with %q", last.Text)
	}
	if captionRuns(model.Message{Text: " \n "}, l, time.Now()) != nil {
		t.Fatal("blank text made a caption")
	}
}
