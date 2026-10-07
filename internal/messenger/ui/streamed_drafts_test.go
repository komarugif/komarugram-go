// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"
	"time"

	"gioui.org/f32"

	"komarugram/internal/messenger/model"
)

// draftSource is a history whose bot streams a draft, which it stops when
// asked, and which keeps the layouts saved.
type draftSource struct {
	benchmarkHistory
	stopped *[]int64
	saved   *[]model.MessageID
}

func (s draftSource) StopStreamedDraft(chat int64) error {
	*s.stopped = append(*s.stopped, chat)
	return nil
}

func (s draftSource) SaveView(_ model.Viewport, layouts []model.MessageLayout) {
	for _, l := range layouts {
		*s.saved = append(*s.saved, l.Key.MessageID)
	}
}

// A draft a bot streams ends the history: Stop stops it in place of Send,
// its buttons do nothing yet, and it is no message to read or to keep the
// layout of.
func TestStreamedDraftInHistory(t *testing.T) {
	h := &chatInputHarness{now: time.Unix(1000, 0)}
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichParagraph, Text: richText("Пишу ответ")},
		{Kind: model.RichButtons, Buttons: []model.RichButton{{Text: richText("Сайт"), Button: model.MessageButton{Kind: "url", URL: "https://example.com"}}}},
	}}
	draft := richMessage(page)
	draft.Key = model.MessageKey{ChatID: 1, MessageID: -1}
	draft.Date, draft.SenderID, draft.Streaming, draft.Stoppable = h.now, 7, true, true
	messages := []model.Message{{Key: model.MessageKey{ChatID: 1, MessageID: 1}, Date: h.now, Text: "Вопрос", ContentRevision: 1}, draft}
	var stopped []int64
	var saved []model.MessageID
	source := draftSource{benchmarkHistory: benchmarkHistory{h: model.History{Messages: messages, Revision: 1}}, stopped: &stopped, saved: &saved}
	h.page = newChatPage(source, func() {})
	t.Cleanup(h.page.Close)
	for range 3 {
		h.frame()
	}
	p := h.page
	if !p.stoppableDraft() {
		t.Fatal("no draft to stop")
	}
	if id := p.bottomMessage(); id != 1 {
		t.Fatalf("the bottom message to read is %d", id)
	}
	r := p.rows[-1]
	if r == nil || !r.streaming {
		t.Fatal("the draft has no streaming row")
	}
	for _, row := range r.articleState.buttons {
		row[0].click.Click()
	}
	h.frame()
	if p.link != "" {
		t.Fatalf("a draft's button asks to open %q", p.link)
	}
	// Stop is at the far right of the floating composer, 56 dp high and
	// 16 dp in from the history's edges, in a slot as wide as it is high.
	h.click(f32.Pt(100+400-16-56/2, float32(p.composer.top+56/2)))
	if len(stopped) != 1 || stopped[0] != 1 {
		t.Fatalf("stopped %v", stopped)
	}
	p.save(true)
	for _, id := range saved {
		if id < 0 {
			t.Fatalf("the layout of draft %d is kept", id)
		}
	}
}
