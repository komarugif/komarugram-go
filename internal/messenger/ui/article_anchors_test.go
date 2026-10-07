// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// anchorPage is a long article whose first paragraph links to an anchor
// in closed details in its middle, and to one it does not have.
func anchorPage(part bool) model.RichPage {
	var link model.RichText
	link.Append(richText("Перейти "))
	from := model.UTF16Len(link.Text)
	link.Append(richText("к концу"))
	link.Mark(from, model.Entity{Kind: "url", URL: "#Deep%20End"})
	link.Append(richText(" или "))
	from = model.UTF16Len(link.Text)
	link.Append(richText("в никуда"))
	link.Mark(from, model.Entity{Kind: "url", URL: "#nowhere"})
	blocks := []model.RichBlock{{Kind: model.RichParagraph, Text: link}}
	for i := range 40 {
		blocks = append(blocks, model.RichBlock{Kind: model.RichParagraph, Text: richText(fmt.Sprintf("Абзац %d", i))})
	}
	blocks = append(blocks, model.RichBlock{Kind: model.RichDetails, Text: richText("Подробнее"), Blocks: []model.RichBlock{
		{Kind: model.RichParagraph, Anchor: "deep end", Text: richText("Глубоко спрятанный абзац")},
	}})
	// Enough under the anchor for the history to scroll it to its top.
	for i := range 40 {
		blocks = append(blocks, model.RichBlock{Kind: model.RichParagraph, Text: richText(fmt.Sprintf("Ещё абзац %d", i))})
	}
	return model.RichPage{Part: part, Blocks: blocks}
}

// anchorHarness is a history of a message, a rich one replying to it, and
// another.
func anchorHarness(t *testing.T, page model.RichPage) *chatInputHarness {
	t.Helper()
	h := &chatInputHarness{now: time.Unix(1000, 0)}
	rich := richMessage(page)
	rich.Key = model.MessageKey{ChatID: 1, MessageID: 2}
	rich.Date = h.now
	// The sender and the message replied to are over the article.
	rich.SenderName, rich.SenderID, rich.ReplyToMessageID = "Бот", 7, 1
	messages := []model.Message{
		{Key: model.MessageKey{ChatID: 1, MessageID: 1}, Date: h.now, Text: "До статьи", ContentRevision: 1},
		rich,
		{Key: model.MessageKey{ChatID: 1, MessageID: 3}, Date: h.now, Text: "После статьи", ContentRevision: 1},
	}
	h.page = newChatPage(benchmarkHistory{h: model.History{Messages: messages, Revision: 1}}, func() {})
	t.Cleanup(h.page.Close)
	h.frame()
	h.seek(1, 0)
	return h
}

// follow activates the link of the rich message that says text.
func (h *chatInputHarness) follow(t *testing.T, text string) {
	t.Helper()
	r := h.page.rows[2]
	for _, run := range r.runs {
		if run.Text == text {
			gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
			h.page.activateRun(gtx, r, run)
			h.frame()
			h.frame()
			return
		}
	}
	t.Fatalf("no run says %q", text)
}

// A link to an anchor opens the details that hide it and scrolls the
// history so that the anchor is at its top, as Telegram Desktop does.
func TestArticleLinkGoesToAnchor(t *testing.T) {
	h := anchorHarness(t, anchorPage(false))
	h.follow(t, "к концу")
	if p := h.page.list.Position; p.First != 1 || p.Offset < 400 {
		t.Fatalf("the history stays at %d+%d", p.First, p.Offset)
	}
	// A double click at the top of the history selects a word of the
	// paragraph the anchor is at.
	at := f32.Pt(100+16+12+20, 8)
	for range 2 {
		h.send(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: pointer.ButtonPrimary})
		h.send(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	}
	if got := h.page.rows[2].selectedText(); got != "Глубоко" {
		t.Fatalf("the top of the history is at %q", got)
	}
}

// A link to an anchor the article does not have tells so; when Telegram
// sent the article cut short, the whole one is opened at it instead.
func TestArticleLinkToMissingAnchor(t *testing.T) {
	h := anchorHarness(t, anchorPage(false))
	h.follow(t, "в никуда")
	if got := h.page.toast.Text(); got != localization.For("en").T("rich.anchor_missing") {
		t.Fatalf("toast %q", got)
	}
	h = anchorHarness(t, anchorPage(true))
	var opened model.MessageKey
	var fragment string
	h.page.openArticle = func(m model.Message, name string) { opened, fragment = m.Key, name }
	h.follow(t, "в никуда")
	if opened.MessageID != 2 || fragment != "nowhere" {
		t.Fatalf("opened %v at %q", opened, fragment)
	}
	if got := h.page.toast.Text(); got != "" {
		t.Fatalf("toast %q", got)
	}
}

// An article Telegram sent cut short has a button under it that shows the
// whole of it; a whole one has none.
func TestArticleShowMore(t *testing.T) {
	for _, part := range []bool{true, false} {
		h := anchorHarness(t, anchorPage(part))
		var opened []model.MessageKey
		h.page.openArticle = func(m model.Message, fragment string) {
			if fragment != "" {
				t.Fatalf("the button opens the article at %q", fragment)
			}
			opened = append(opened, m.Key)
		}
		h.seek(1, 2000)
		h.page.rows[2].articleState.more.click.Click()
		h.frame()
		if part && (len(opened) != 1 || opened[0].MessageID != 2) || !part && len(opened) != 0 {
			t.Fatalf("part %v: the button opened %v", part, opened)
		}
	}
}

// An anchor inside a long paragraph is at its line, not at the top of the
// paragraph.
func TestInlineAnchorAtItsLine(t *testing.T) {
	var p model.RichText
	p.Append(richText(strings.Repeat("word ", 60)))
	p.AddAnchor("middle")
	p.Append(richText(strings.Repeat("word ", 60)))
	page := model.RichPage{Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: p}}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	h.frame()
	y, ok := h.row.articleState.tops["middle"]
	if !ok || y <= 0 {
		t.Fatalf("the anchor is at %d (%v)", y, ok)
	}
	if want, _ := lineTop(h.row.text.fragments, 300); y != want {
		t.Fatalf("the anchor is at %d, its line at %d", y, want)
	}
}

// An anchor at the end of a paragraph is at its last line.
func TestInlineAnchorAtTheEnd(t *testing.T) {
	p := richText(strings.Repeat("word ", 80) + "end")
	p.AddAnchor("last")
	page := model.RichPage{Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: p}}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	h.frame()
	last := h.row.text.fragments[len(h.row.text.fragments)-1].Bounds.Min.Y
	if y := h.row.articleState.tops["last"]; y != last || y == 0 {
		t.Fatalf("the anchor is at %d, the last line at %d", y, last)
	}
}
