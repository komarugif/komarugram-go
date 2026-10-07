// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// measuredRow lays out m between two messages of the same day, in a
// history width px wide, and returns how high its row is, and the guess of
// it before it was laid out.
func measuredRow(t *testing.T, m model.Message, width int) (measured, guessed int) {
	t.Helper()
	now := time.Unix(1_790_000_000, 0)
	m.Key, m.Date = model.MessageKey{ChatID: 1, MessageID: 2}, now
	plain := func(id model.MessageID) model.Message {
		return model.Message{Key: model.MessageKey{ChatID: 1, MessageID: id}, Date: now, Text: "x", ContentRevision: 1}
	}
	p := newChatPage(articleStore{benchmarkHistory{h: model.History{Messages: []model.Message{plain(1), m, plain(3)}, Revision: 1}}}, func() {})
	p.images = &imageOps{}
	p.openArticle = func(model.Message, string) {}
	t.Cleanup(p.Close)
	for range 3 {
		gtx := layout.Context{Ops: new(op.Ops), Now: now, Constraints: layout.Exact(image.Pt(width, 1200)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 1, 0, true
		p.Layout(gtx, model.Chat{ID: 1}, localization.For("ru"), false)
		if guessed == 0 {
			guessed = p.articleHeights[2].height
		}
	}
	return p.measures[2].HeightPx, guessed
}

// Until a rich message's row is laid out, the history guesses its height
// from the article's blocks, its media exactly: within a fifth of it.
func TestArticleHeightGuess(t *testing.T) {
	pages := map[string]model.RichPage{
		"demo":    mockstore.RichExample(false),
		"part":    mockstore.RichExample(true),
		"all":     articleFixture(),
		"anchors": anchorPage(false),
	}
	for name, page := range pages {
		for _, width := range []int{400, 700, 1000} {
			measured, guessed := measuredRow(t, richMessage(page), width)
			if guessed == 0 || measured == 0 {
				t.Fatalf("%s at %d: measured %d, guessed %d", name, width, measured, guessed)
			}
			if off := float64(guessed-measured) / float64(measured); off > .2 || off < -.2 {
				t.Errorf("%s at %d: guessed %d, measured %d (%+.0f%%)", name, width, guessed, measured, off*100)
			} else {
				t.Logf("%s at %d: guessed %d, measured %d (%+.0f%%)", name, width, guessed, measured, off*100)
			}
		}
	}
}

// countingStore draws an article's photos as articleStore does, and counts
// the media asked for, by id.
type countingStore struct {
	articleStore
	mu    *sync.Mutex
	asked map[string]bool
}

func (s countingStore) Media(ctx context.Context, m model.Message) ([]byte, error) {
	if m.Media != nil {
		s.mu.Lock()
		s.asked[m.Media.ID] = true
		s.mu.Unlock()
	}
	return s.articleStore.Media(ctx, m)
}

// An article's photos load when they come near the view, not all at once.
func TestArticleLoadsMediaNearTheView(t *testing.T) {
	var blocks []model.RichBlock
	for i := range 24 {
		blocks = append(blocks, model.RichBlock{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: fmt.Sprintf("p%d", i), MIMEType: "image/png", Width: 640, Height: 360}}}})
	}
	m := richMessage(model.RichPage{Blocks: blocks})
	now := time.Unix(1_790_000_000, 0)
	m.Key, m.Date = model.MessageKey{ChatID: 1, MessageID: 1}, now
	store := countingStore{mu: new(sync.Mutex), asked: map[string]bool{}}
	store.h = model.History{Messages: []model.Message{m}, Revision: 1}
	p := newChatPage(store, func() {})
	p.images = &imageOps{}
	t.Cleanup(p.Close)
	frame := func() {
		gtx := layout.Context{Ops: new(op.Ops), Now: now, Constraints: layout.Exact(image.Pt(500, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		p.images.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1}, localization.For("ru"), false)
		p.images.EndFrame()
	}
	asked := func() map[string]bool {
		// The loads run apart from the frame.
		time.Sleep(200 * time.Millisecond)
		store.mu.Lock()
		defer store.mu.Unlock()
		return maps.Clone(store.asked)
	}
	// The chat opens at its end: the last photos load.
	for range 3 {
		frame()
	}
	got := asked()
	if !got["p23"] || got["p0"] || len(got) > 8 {
		t.Fatalf("at the end, asked for %d photos: %v", len(got), got)
	}
	// At the top, the first ones; those in the middle, far from either,
	// not yet.
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 0, 0, true
	for range 3 {
		frame()
	}
	got = asked()
	if !got["p0"] || got["p12"] || len(got) > 16 {
		t.Fatalf("at the top, asked for %d photos: %v", len(got), got)
	}
}

// BenchmarkLongArticleFrame lays out a history frame that shows an article
// of 500 blocks, the most Telegram allows: the whole of it is laid out each
// frame (2.6 ms and 0.6 MB a frame here, 2026-10-06).
func BenchmarkLongArticleFrame(b *testing.B) {
	var blocks []model.RichBlock
	for i := range 500 {
		switch i % 5 {
		case 0:
			blocks = append(blocks, model.RichBlock{Kind: model.RichHeading, Level: 2, Text: model.RichText{Text: fmt.Sprintf("Раздел %d", i)}})
		case 4:
			blocks = append(blocks, model.RichBlock{Kind: model.RichList, Items: []model.RichListItem{{Text: model.RichText{Text: "пункт"}}, {Text: model.RichText{Text: "второй"}}}})
		default:
			blocks = append(blocks, model.RichBlock{Kind: model.RichParagraph, Text: model.RichText{Text: strings.Repeat("Текст абзаца статьи, ", 3)}})
		}
	}
	m := richMessage(model.RichPage{Blocks: blocks})
	now := time.Unix(1_790_000_000, 0)
	m.Key, m.Date = model.MessageKey{ChatID: 1, MessageID: 1}, now
	p := newChatPage(articleStore{benchmarkHistory{h: model.History{Messages: []model.Message{m}, Revision: 1}}}, func() {})
	p.images = &imageOps{}
	defer p.Close()
	ops := new(op.Ops)
	base := layout.Context{Ops: ops, Now: now, Constraints: layout.Exact(image.Pt(700, 800)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	theme := defaults.NewTheme(base, schemes.SchemeBaselineLight())
	frame := func() {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: now, Constraints: layout.Exact(image.Pt(700, 800)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		p.Layout(gtx, model.Chat{ID: 1}, localization.For("ru"), false)
	}
	frame()
	frame()
	b.ResetTimer()
	for range b.N {
		frame()
	}
}
