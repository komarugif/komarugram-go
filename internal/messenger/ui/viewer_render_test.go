// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"image/png"
	"os"
	"strconv"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"
	"gio-mw/widget/indicator"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestRenderMediaViewer draws the viewer over the demo photos on the GPU
// and saves a screenshot, for looking at it rather than asserting:
//
//	VIEWER_PNG=/tmp/viewer.png go test ./internal/messenger/ui -run RenderPhotoViewer
//
// With VIEWER_PROFILE set it shows the photos of a chat's profile, with
// VIEWER_GIFS a GIF of the chat among its GIFs, with VIEWER_STORIES the
// stories of someone of the list's, and with VIEWER_SAVE_GIF a video saved
// as a GIF by the bot, halfway through.
func TestRenderMediaViewer(t *testing.T) {
	out := os.Getenv("VIEWER_PNG")
	if out == "" {
		t.Skip("set VIEWER_PNG to a file name")
	}
	store := mockstore.New(time.Now(), 0)
	h := store.History(2)
	var start model.Message
	for _, m := range h.Messages {
		if m.Kind == model.MessagePhoto && m.Media != nil && len(m.Media.Variants) > 0 {
			start = m
			if m.Key.MessageID > 100 {
				break
			}
		}
	}
	var images imageOps
	changed := make(chan struct{}, 64)
	v := newMediaViewer(store, &images, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer v.Destroy()
	v.popout = func(int64, model.Message, mediaList) {}
	v.share.chats = store.Chats
	if os.Getenv("VIEWER_STORIES") != "" {
		peer := store.StoryPeers()[1]
		stories, _ := store.PeerStories(context.Background(), peer.ID)
		start = stories[0]
		v.OpenStories(peer.ID, start.Key.MessageID)
	} else if os.Getenv("VIEWER_PROFILE") != "" {
		// The photos of the chat's profile, as its avatar opens them.
		start, _ = store.ProfilePhoto(2)
		v.OpenProfile(2)
	} else if os.Getenv("VIEWER_GIFS") != "" {
		for _, m := range h.Messages {
			if m.Kind == model.MessageGIF && m.Media != nil && m.Media.ID == "demo/gif" {
				start = m
				if m.Key.MessageID > 100 {
					break
				}
			}
		}
		v.OpenGIF(2, start, nil)
	} else if os.Getenv("VIEWER_SAVE_GIF") != "" {
		for _, m := range h.Messages {
			if m.Kind == model.MessageVideo && m.Media != nil {
				start = m
				break
			}
		}
		v.Open(2, start, nil)
		v.gif.work = &gifWork{stop: func() {}, done: make(chan error, 1), bot: true,
			progress: model.GIFProgress{Stage: model.GIFEncoding, Share: .42, Pass: 1}}
		v.gif.bar = indicator.Linear()
		v.gif.modal.Open()
	} else {
		v.Open(2, start, nil)
	}
	size := image.Pt(1280, 800)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := new(op.Ops)
	frame := func() {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		images.BeginFrame()
		v.Layout(gtx, localization.For("ru"), false)
		images.EndFrame()
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		frame()
		st := v.full.StatusFit(start, false, image.Pt(1, 1), false)
		if target, ok := v.viewerTarget(start); ok && start.Kind == model.MessageVideo {
			st = v.full.StatusFit(target, false, image.Pt(1, 1), false)
		}
		if st.Frame != nil {
			time.Sleep(300 * time.Millisecond) // let thumbnails finish
			break
		}
		select {
		case <-changed:
		case <-time.After(50 * time.Millisecond):
		}
	}
	// VIEWER_ZOOM=2 shows the photo twice its own size around a point
	// right of and above the centre.
	if zoom, _ := strconv.ParseFloat(os.Getenv("VIEWER_ZOOM"), 32); zoom > 0 {
		v.zoom.zoomBy(float32(zoom), f32.Pt(250, -120), v.zoom.target)
		v.zoom.scale, v.zoom.target = 0, 0
		fit := fitScale(native(start), image.Pt(size.X-2*int(float32(viewerSide)*1.25), size.Y-int(float32(viewerBar+viewerStrip)*1.25)))
		v.zoom.zoomBy(float32(zoom)/fit, f32.Pt(250, -120), fit)
		for i := 0; i < 200 && v.full.StatusFit(start, false, native(start), false).Frame == nil; i++ {
			frame()
			time.Sleep(20 * time.Millisecond)
		}
		for i := 0; i < 5; i++ {
			frame()
			time.Sleep(20 * time.Millisecond)
		}
	}
	frame()
	frame()
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
