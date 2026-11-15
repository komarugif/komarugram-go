// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// The snapshot lays out the messages as the options ask: the date and
// reactions go when they are not wanted.
func TestBuildSnapshot(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[2].Reactions = []model.Reaction{{Emoji: "❤", Count: 3}}
	})
	p := h.page
	msgs := []model.Message{p.messages[1], p.messages[2]}
	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(500, 700)), Values: map[string]any{}}
	gtx.Metric.PxPerDp, gtx.Metric.PxPerSp = 1, 1
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	_, full, ok := p.buildSnapshot(gtx, localization.For("en"), msgs, shotOptions{date: true, reactions: true})
	if !ok {
		t.Fatal("two messages are too tall")
	}
	_, bare, _ := p.buildSnapshot(gtx, localization.For("en"), msgs, shotOptions{})
	if bare.Y >= full.Y || full.X != 500 {
		t.Fatalf("without the date and reactions the snapshot is %v, with them %v", bare, full)
	}
	if p.snapshotting {
		t.Fatal("the history is left snapshotting")
	}
}

// TestRenderSnapshotDialog saves the snapshot dialog, for looking at it:
// SHOT_PNG=/tmp/shot.png; SHOT_THEME=dark draws the snapshot dark.
func TestRenderSnapshotDialog(t *testing.T) {
	path := os.Getenv("SHOT_PNG")
	if path == "" {
		t.Skip("set SHOT_PNG to a file")
	}
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[2].Reactions = []model.Reaction{{Emoji: "❤", Count: 3}}
		messages[3].Outgoing = true
	})
	p := h.page
	p.shot.open(p, []model.Message{p.messages[1], p.messages[2], p.messages[3]})
	if os.Getenv("SHOT_THEME") == "dark" {
		p.shot.themes.SetValue(shotDark)
		p.shot.opts.theme = shotDark
	}
	l := localization.For("ru")
	// The preview renders first: two headless contexts at once do not
	// go together in a test, which draws the window headless too.
	deadline := time.Now().Add(10 * time.Second)
	for p.shot.image == nil && p.shot.err == nil {
		if time.Now().After(deadline) {
			t.Fatal("the preview did not render")
		}
		gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 800)), Values: map[string]any{}}
		gtx.Metric.PxPerDp, gtx.Metric.PxPerSp = 1, 1
		wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
		p.shot.layout(gtx, p, l)
		time.Sleep(10 * time.Millisecond)
	}
	renderFrames(t, image.Pt(600, 800), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.layoutDialogs(gtx, l)
		p.media.EndFrame()
		p.images.EndFrame()
		time.Sleep(10 * time.Millisecond)
	})
}
