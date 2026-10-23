// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// What a frame draws again and again allocates nothing once it was drawn:
// each of these made garbage in every frame, for each message or once.
func TestFrameHelpersDoNotAllocate(t *testing.T) {
	gtx := layout.Context{Ops: new(op.Ops), Values: map[string]any{}}
	l := localization.For("en")
	m := model.Message{Date: time.Date(2026, 10, 13, 7, 30, 0, 0, time.UTC), EditedAt: time.Date(2026, 10, 13, 7, 31, 0, 0, time.UTC)}
	look := preferences.Look{BubbleRadius: 12, EditedMark: "✎"}
	var sink layout.FlexChild
	for name, f := range map[string]func(){
		"the line of a message's time": func() { footerText(gtx, m, l) },
		"the look in Values":           func() { withLook(gtx, look) },
		"a spacer":                     func() { sink = vspace(8) },
	} {
		f()
		if n := testing.AllocsPerRun(100, f); n != 0 {
			t.Errorf("%s: %v allocations, want none", name, n)
		}
	}
	_ = sink
}

// The values of a context with the chat's colors are made once, and filled
// again in the next frames rather than made anew.
func TestThemedValuesAreReusedAcrossFrames(t *testing.T) {
	ctl := &chatThemeController{}
	gtx := layout.Context{Ops: new(op.Ops), Values: map[string]any{}, Now: time.Now()}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	edit := func(sc *token.Scheme) { sc.Primary.Color = token.MatColor{R: 1, A: 255} }
	frame := func() {
		gtx.Now = gtx.Now.Add(time.Second / 60)
		themed := ctl.themed(gtx, historyValues, edit)
		if wdk.GetMaterialTheme(themed).Scheme.Primary.Color.R != 1 {
			t.Fatal("the scheme was not changed")
		}
	}
	frame()
	if n := testing.AllocsPerRun(100, frame); n != 0 {
		t.Errorf("%v allocations a frame, want none", n)
	}
}

// A voice message's bars are worked out of its waveform once.
func TestWaveformBarsAreKept(t *testing.T) {
	var r audioRow
	waveform := []byte{0x1f, 0x3e, 0x7c, 0xf8, 0xf0}
	first := r.waveformBars(waveform)
	if again := r.waveformBars(waveform); &again[0] != &first[0] {
		t.Error("the bars were worked out again for the same waveform")
	}
	if other := r.waveformBars([]byte{1, 2, 3, 4, 5}); &other[0] == &first[0] {
		t.Error("the bars of another waveform are the old ones")
	}
}

// A frame of the history allocates a fraction of what it did: messages
// captured by closures were moved to the heap (480 bytes each), the lists
// of a bubble and of a text flow grew from nothing, and the jump buttons
// made closures over the history. 279 allocations a frame before, 144
// after (2026-10-13); the bound leaves room for what comes, not for those.
// It is of a row in view: when the time went on the text's last line, the
// rows got shorter, 16 in view where 11 were, and the frame's count rose
// to 207 while a row's fell from 14.9 to 12.9 (2026-10-22).
func TestHistoryFrameAllocations(t *testing.T) {
	source := benchmarkHistory{h: model.History{Revision: 1, Messages: make([]model.Message, 1000)}}
	for i := range source.h.Messages {
		source.h.Messages[i] = model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i + 1)}, Text: "A measured message with enough text to wrap once in the conversation view.", Date: time.Unix(1700000000+int64(i), 0), ContentRevision: 1}
	}
	p := newChatPage(source, func() {})
	defer p.Close()
	var images imageOps
	p.images = &images
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Now: time.Now(), Constraints: layout.Exact(image.Pt(800, 720)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: make(map[string]any)}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
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
	n := testing.AllocsPerRun(20, render)
	rows := max(1, p.list.Position.Count)
	if perRow := n / float64(rows); perRow > 16 {
		t.Errorf("%v allocations a frame of the history, %.1f for each of its %d rows, want at most 16", n, perRow, rows)
	}
}
