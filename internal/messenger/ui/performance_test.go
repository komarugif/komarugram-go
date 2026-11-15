// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"strings"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

var benchImageOp paint.ImageOp

// A decoded JPEG is YCbCr. This models one 1024px photo in the old frame path.
func BenchmarkPhotoImageOpPerFrame(b *testing.B) {
	im := image.NewYCbCr(image.Rect(0, 0, 1024, 768), image.YCbCrSubsampleRatio420)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchImageOp = paint.NewImageOp(im)
	}
}
func BenchmarkPhotoImageOpCached(b *testing.B) {
	im := image.NewYCbCr(image.Rect(0, 0, 1024, 768), image.YCbCrSubsampleRatio420)
	var cache imageOps
	cache.BeginFrame()
	cache.Op(im)
	cache.EndFrame()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		cache.BeginFrame()
		benchImageOp = cache.Op(im)
		cache.EndFrame()
	}
}
func TestImageTextureStableAcrossFrames(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var cache imageOps
	cache.BeginFrame()
	first := cache.Op(im)
	cache.EndFrame()
	for i := 0; i < 10; i++ {
		cache.BeginFrame()
		if cache.Op(im) != first {
			t.Fatal("texture identity changed")
		}
		cache.EndFrame()
	}
	cache.BeginFrame()
	cache.EndFrame()
	cache.BeginFrame()
	cache.EndFrame()
	if len(cache.entries) != 0 {
		t.Fatal("invisible texture retained")
	}
}

type benchmarkHistory struct {
	model.ConversationStore
	h model.History
}

func (s benchmarkHistory) OpenChat(int64) {}
func (s benchmarkHistory) HistorySince(_ int64, revision uint64) (model.History, bool) {
	h := s.h
	if revision == h.Revision {
		h.Messages = nil
		return h, false
	}
	return h, true
}
func (s benchmarkHistory) Layouts(int64, model.RenderEnvironment) []model.MessageLayout { return nil }
func (s benchmarkHistory) Viewport(int64) (model.Viewport, bool)                        { return model.Viewport{}, false }
func (s benchmarkHistory) SaveView(model.Viewport, []model.MessageLayout)               {}
func BenchmarkHistoryLayout(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			source := benchmarkHistory{h: model.History{Revision: 1, Messages: make([]model.Message, n)}}
			for i := range source.h.Messages {
				source.h.Messages[i] = model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i + 1)}, Text: "A measured message with enough text to wrap once in the conversation view.", Date: time.Unix(1700000000+int64(i), 0), ContentRevision: 1}
			}
			p := newChatPage(source, func() {})
			defer p.Close()
			var images imageOps
			p.images = &images
			var ops op.Ops
			gtx := layout.Context{Ops: &ops, Now: time.Now(), Constraints: layout.Exact(image.Pt(800, 720)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: make(map[string]any)}
			wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
			render := func() {
				ops.Reset()
				images.BeginFrame()
				p.media.BeginFrame()
				p.Layout(gtx, model.Chat{ID: 1}, localization.For(string(localization.English)), false)
				p.media.EndFrame()
				images.EndFrame()
			}
			render()
			render()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				render()
			}
		})
	}
}

// Long channel posts wrap into dozens of lines. Resizing changes the width on
// every frame, so no shaped line can come from the previous one.
func BenchmarkHistoryLongPosts(b *testing.B) {
	post := strings.Repeat("Длинный пост канала с обычным текстом, который переносится на много строк при чтении. ", 16)
	for _, resize := range []bool{false, true} {
		name := "steady"
		if resize {
			name = "resize"
		}
		b.Run(name, func(b *testing.B) {
			source := benchmarkHistory{h: model.History{Revision: 1, Messages: make([]model.Message, 200)}}
			for i := range source.h.Messages {
				text := post
				var entities []model.Entity
				if i%2 == 1 {
					// A bold phrase in the middle splits the text into three spans.
					entities = []model.Entity{{Kind: "bold", Offset: 300, Length: 40}}
				}
				source.h.Messages[i] = model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i + 1)}, Text: text, Entities: entities, Date: time.Unix(1700000000+int64(i), 0), ContentRevision: 1}
			}
			p := newChatPage(source, func() {})
			defer p.Close()
			var images imageOps
			p.images = &images
			var ops op.Ops
			gtx := layout.Context{Ops: &ops, Now: time.Now(), Constraints: layout.Exact(image.Pt(800, 720)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: make(map[string]any)}
			wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
			width := 800
			render := func() {
				if resize {
					width = 600 + (width-599)%300
					gtx.Constraints = layout.Exact(image.Pt(width, 720))
				}
				ops.Reset()
				images.BeginFrame()
				p.media.BeginFrame()
				p.Layout(gtx, model.Chat{ID: 1}, localization.For(string(localization.English)), false)
				p.media.EndFrame()
				images.EndFrame()
			}
			render()
			render()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				render()
			}
		})
	}
}

// Unmeasured heights are estimated from the text; the count kept for the
// estimate follows an edit.
func TestEstimateFollowsEdits(t *testing.T) {
	p := newChatPage(benchmarkHistory{}, func() {})
	defer p.Close()
	env := model.RenderEnvironment{ScaleMilli: 1000}
	m := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 1}, Text: "short", ContentRevision: 1}
	p.rebuild([]model.Message{m}, env)
	short := p.heights.Prefix(1)
	m.Text, m.ContentRevision = strings.Repeat("long text ", 100), 2
	p.rebuild([]model.Message{m}, env)
	if long := p.heights.Prefix(1); long <= short {
		t.Fatalf("estimate %d after the edit, %d before", long, short)
	}
}
