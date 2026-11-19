// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"strings"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/chattheme"
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
	if bare.Y >= full.Y {
		t.Fatalf("without the date and reactions the snapshot is %v, with them %v", bare, full)
	}
	if p.snapshotting {
		t.Fatal("the history is left snapshotting")
	}
}

func TestSingleMessageShotFitsContent(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[2].Outgoing = true
	})
	p := h.page
	gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Exact(image.Pt(500, 700)), Values: map[string]any{}}
	gtx.Metric.PxPerDp, gtx.Metric.PxPerSp = 1, 1
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	l := localization.For("en")
	wallpaperColor := uint32(0x336699)
	wallpaper, err := chattheme.Prepare(&model.ChatWallpaper{Colors: []uint32{wallpaperColor}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.appearance = &chatThemeController{images: p.images, background: wallpaper, image: wallpaper.Render(image.Pt(500, 700)), size: image.Pt(500, 700), invalidate: p.invalidate}
	for _, msg := range p.messages[1:3] {
		p.shot.open(p, []model.Message{msg})
		p.shot.render(gtx, p, l)
		select {
		case r := <-p.shot.results:
			if r.err != nil {
				t.Fatal(r.err)
			}
			if w := r.image.Bounds().Dx(); w == 0 || w >= 250 {
				t.Errorf("outgoing=%v: a short message with a date takes %d pixels", msg.Outgoing, w)
			}
			if got := r.image.RGBAAt(0, 0); got != color.RGBA(chattheme.RGB(wallpaperColor)) {
				t.Errorf("outgoing=%v: expected the wallpaper at the outer margin, got %v", msg.Outgoing, got)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the snapshot did not render")
		}
	}
}

func TestMessageShotWidth(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		scale      float32
		maxWidth   int
	}{
		{name: "short", text: "Hi", scale: 1, maxWidth: 400},
		{name: "wide", text: strings.Repeat("A long message ", 20), scale: 1.5, maxWidth: 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
				messages[1].Text, messages[2].Text = tc.text, tc.text
				messages[2].Outgoing = true
			})
			h.scale, h.still = tc.scale, true
			p := h.page
			var narrow *image.RGBA
			for _, width := range []int{480, 2400} {
				h.width = width
				h.frame()
				p.shot.open(p, p.messages[1:3])
				waitFor(t, "message snapshot", h.frame, func() bool { return p.shot.image != nil || p.shot.err != nil })
				if p.shot.err != nil {
					t.Fatal(p.shot.err)
				}
				shot := p.shot.image
				if shot.Bounds().Dx() > tc.maxWidth {
					t.Errorf("window %d: snapshot width %d exceeds %d", width, shot.Bounds().Dx(), tc.maxWidth)
				}
				if narrow == nil {
					narrow = shot
				} else if narrow.Bounds() != shot.Bounds() || !bytes.Equal(narrow.Pix, shot.Pix) {
					t.Errorf("the same snapshot changed from %v to %v when the window widened", narrow.Bounds().Size(), shot.Bounds().Size())
				}
			}
		})
	}
}

// TestRenderSnapshotDialog saves the snapshot dialog, for looking at it:
// SHOT_PNG=/tmp/shot.png; SHOT_THEME=dark draws the snapshot dark,
// SHOT_SINGLE=incoming or outgoing shows one message.
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
	msgs := p.messages[1:4]
	switch os.Getenv("SHOT_SINGLE") {
	case "incoming":
		msgs = msgs[1:2]
	case "outgoing":
		msgs = msgs[2:3]
	}
	p.shot.open(p, msgs)
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
