// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/styledtext"
)

// A draft types itself in: the caret starts at its first line, catches up
// with all of it in about typingCatchUp, and goes on from where it is when
// more text comes in.
func TestDraftTypesItselfIn(t *testing.T) {
	text := "Первая строка черновика, которую бот пишет по словам. Вторая строка, ещё длиннее первой, чтобы перенестись. И третья."
	m := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: -1}, SenderID: 7, Text: text, ContentRevision: 1, Streaming: true}
	h := newEntityHarness(t, m, model.KindUser)
	h.animate = true
	h.frame()
	ty := h.page.typing[-1]
	if ty == nil {
		t.Fatal("a draft has no caret")
	}
	lines := typingLines(h.row.text.fragments)
	if len(lines) < 2 {
		t.Fatalf("%d lines", len(lines))
	}
	if ty.line != 0 {
		t.Fatalf("the caret starts at line %d", ty.line)
	}
	first := h.row.text.size.Y
	for range int(typingCatchUp/(16*time.Millisecond)) / 2 {
		h.frame()
	}
	if ty.line == 0 && ty.x == 0 || ty.line >= len(lines) {
		t.Fatalf("halfway, the caret is at line %d of %d", ty.line, len(lines))
	}
	for range int(typingCatchUp / (16 * time.Millisecond)) {
		h.frame()
	}
	if ty.line != len(lines) {
		t.Fatalf("the caret stopped at line %d of %d", ty.line, len(lines))
	}
	// The text shows down to the caret's line, and grows with it.
	for range 30 {
		h.frame()
	}
	if whole := lines[len(lines)-1].bottom; first > lines[0].bottom || h.row.text.size.Y != whole {
		t.Fatalf("the text showed %d px high at first, %d at the end, of %d", first, h.row.text.size.Y, whole)
	}
	// More text: the caret goes on from the end of what was.
	m.Text, m.ContentRevision = text+" Ещё одна строка пришла позже, с новыми словами.", 2
	h.m = m
	h.row = newMessageRow(m, h.l, h.now)
	h.frame()
	if ty.line < len(lines)-1 {
		t.Fatalf("with more text the caret went back to line %d", ty.line)
	}
	if h.page.typing[-1] != ty {
		t.Fatal("a draft at its end lost its caret")
	}
}

// The message a draft becomes takes its caret over; other messages type
// nothing, and a draft that goes without its message takes its caret along.
func TestTypingHandedOver(t *testing.T) {
	p := newChatPage(&entityStore{}, func() {})
	t.Cleanup(p.Close)
	draft := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: -3}, SenderID: 7, Streaming: true}
	old := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: 40}, SenderID: 7}
	p.messages = []model.Message{old, draft}
	caret := &typing{sender: 7, line: 2}
	p.typing = map[model.MessageID]*typing{-3: caret}
	other := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: 41}, SenderID: 8}
	became := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: 42}, SenderID: 7}
	p.handOverTyping([]model.Message{old, became, other})
	if p.typing[42] != caret || caret.line != 2 || len(p.typing) != 1 {
		t.Fatalf("carets %v", p.typing)
	}
	p.messages = []model.Message{old, became, other}
	p.typing = map[model.MessageID]*typing{-4: {sender: 9}}
	p.handOverTyping([]model.Message{old, became, other})
	if len(p.typing) != 0 {
		t.Fatalf("a draft gone without its message left %v", p.typing)
	}
}

// Fragments are lines by their middles, top to bottom, however they come.
func TestTypingLines(t *testing.T) {
	frag := func(x0, y0, x1, y1 int) styledtext.Fragment {
		return styledtext.Fragment{Bounds: image.Rect(x0, y0, x1, y1)}
	}
	lines := typingLines([]styledtext.Fragment{frag(0, 20, 50, 40), frag(0, 0, 30, 20), frag(30, 2, 80, 18), frag(0, 39, 10, 60)})
	want := []typingLine{{0, 20, 0, 80}, {20, 40, 0, 50}, {39, 60, 0, 10}}
	if len(lines) != len(want) {
		t.Fatalf("lines %v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("lines %v, want %v", lines, want)
		}
	}
}

// Text that comes in after the caret reached the end sets its speed again:
// a short draft types slowly, and what comes after it catches up as fast.
func TestTypingCatchesUpWithNewText(t *testing.T) {
	m := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: -1}, SenderID: 7, Text: "Ну", ContentRevision: 1, Streaming: true}
	h := newEntityHarness(t, m, model.KindUser)
	h.animate = true
	for range 120 {
		h.frame()
	}
	m.Text, m.ContentRevision = "Ну что ж, вот длинный ответ бота, который пришёл весь сразу и занимает несколько строк, чтобы каретке было куда идти.", 2
	h.m = m
	h.row = newMessageRow(m, h.l, h.now)
	h.frame()
	lines := len(typingLines(h.row.text.fragments))
	for range int(typingCatchUp/(16*time.Millisecond)) * 3 / 2 {
		h.frame()
	}
	if ty := h.page.typing[-1]; ty.line != lines {
		t.Fatalf("the caret is at line %d of %d", ty.line, lines)
	}
}

// As in Telegram for Android, a caret that reached the end stays at the
// end of the last line: of an answer that replaces "Analyzing..." the part
// as wide shows at once and the rest types itself in, and words added to
// that line type themselves in too.
func TestTypingFollowsEdits(t *testing.T) {
	m := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: -1}, SenderID: 7, Text: "Analyzing...", ContentRevision: 1, Streaming: true}
	h := newEntityHarness(t, m, model.KindUser)
	h.animate = true
	for range 120 {
		h.frame()
	}
	ty := h.page.typing[-1]
	if ty.line != 1 {
		t.Fatalf("the caret is at line %d of the first draft", ty.line)
	}
	analyzing := float32(ty.lines[0].right - ty.lines[0].left)
	next := func(text string, revision uint64) {
		m.Text, m.ContentRevision = text, revision
		h.m = m
		h.row = newMessageRow(m, h.l, h.now)
		h.frame()
	}
	next("I'll demonstrate every block", 2)
	if ty.line != 0 || ty.x < analyzing-1 || ty.x > analyzing+20 {
		t.Fatalf("after an edit the caret is at line %d, %v px in, not at %v", ty.line, ty.x, analyzing)
	}
	for range 120 {
		h.frame()
	}
	next("I'll demonstrate every block type", 3)
	width := float32(ty.lines[0].right - ty.lines[0].left)
	if ty.line != 0 || ty.x >= width {
		t.Fatalf("with words added to its line the caret is at line %d, %v px of %v", ty.line, ty.x, width)
	}
}
