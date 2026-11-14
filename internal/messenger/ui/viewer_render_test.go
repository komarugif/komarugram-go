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
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestRenderMediaViewer draws the viewer over the demo photos on the GPU
// and saves a screenshot, for looking at it rather than asserting:
//
//	VIEWER_PNG=/tmp/viewer.png go test ./internal/messenger/ui -run RenderPhotoViewer
//
// With VIEWER_PROFILE set it shows the photos of a chat's profile (=plays, its first
// playing its video, with the H.264 decoder that needs KOMARUGRAM_AVCDEC), with
// VIEWER_GIFS a GIF of the chat among its GIFs, with VIEWER_STORIES the
// stories of someone of the list's, and with VIEWER_SAVE_GIF a video saved
// as a GIF by the bot, halfway through, and with VIEWER_BOX the files of the
// box of files about to go. VIEWER_EDIT opens the media editor on the photo:
// paint, crop, text, shapes, stickers, turned, trim, opening, opening-late,
// choice, work, failed, discard. With VIEWER_DARK its dialogs are a dark theme's.
// VIEWER_PROFILE=group opens a group's photos, which the account may change;
// VIEWER_AVATAR and VIEWER_TIP: see below.
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
	var source model.ConversationStore = store
	var boxItems []model.Message
	if os.Getenv("VIEWER_BOX") != "" {
		files := boxFixture(t)
		source, boxItems, start = boxGallery(store, 2, files, files[2])
	}
	v := newMediaViewer(source, &images, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer v.Destroy()
	if box, ok := source.(*boxMedia); ok {
		v.editSink = box.edit
	}
	v.popout = func(int64, model.Message, mediaList) {}
	// VIEWER_BLUR: the plates of the caption and of a video's controls blur
	// what is under them, as with the toasts' blur on.
	if os.Getenv("VIEWER_BLUR") != "" {
		v.overlays = func() overlayPrefs { return overlayPrefs{toasts: true} }
	}
	v.share.chats = store.Chats
	if boxItems != nil {
		v.popout = nil
		v.openList(2, start, mediaList{items: boxItems, ended: [2]bool{true, true}, gallery: galleryBox})
	} else if os.Getenv("VIEWER_STORIES") != "" {
		peer := store.StoryPeers()[1]
		stories, _ := store.PeerStories(context.Background(), peer.ID)
		start = stories[0]
		v.OpenStories(peer.ID, start.Key.MessageID)
	} else if os.Getenv("VIEWER_PROFILE") != "" {
		// The photos of the chat's profile, as its avatar opens them.
		if os.Getenv("VIEWER_PROFILE") == "plays" {
			// The demo's video is read from the root of the repository.
			t.Chdir("../../..")
			store.ProfileVideos = true
		}
		// VIEWER_PROFILE=group: a group's, whose photos the account may change.
		profile := int64(2)
		if os.Getenv("VIEWER_PROFILE") == "group" {
			profile = 3
		}
		start, _ = store.ProfilePhoto(profile)
		v.OpenProfile(profile)
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
	edit := os.Getenv("VIEWER_EDIT")
	size := image.Pt(1280, 800)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := new(op.Ops)
	// Input as a window has it: what is disabled shows no tooltip.
	var router input.Router
	frame := func() {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Source: router.Source(), Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
		scheme := schemes.SchemeBaselineLight()
		if os.Getenv("VIEWER_DARK") != "" {
			// The viewer is dark in either; its dialogs are the theme's.
			scheme = schemes.SchemeBaselineDark()
		}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, scheme))
		images.BeginFrame()
		v.Layout(gtx, localization.For("ru"), false)
		images.EndFrame()
		router.Frame(ops)
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
	if motion, ok := motionOf(start); ok && os.Getenv("VIEWER_PROFILE") == "plays" {
		for i := 0; i < 1000 && v.full.GIFPosition(motion) < 2*time.Second; i++ {
			frame()
			time.Sleep(10 * time.Millisecond)
		}
	}
	// VIEWER_TIP shows the tooltip of the bar's first button, as the pointer left it
	// there a second ago; VIEWER_AVATAR=delete asks whether to delete the photo, and
	// VIEWER_AVATAR=main shows the second photo, which may become the main one.
	if os.Getenv("VIEWER_AVATAR") == "main" {
		items, _, _, _, _ := v.snapshot()
		for i := 0; len(items) < 2 && i < 200; i++ {
			frame()
			time.Sleep(10 * time.Millisecond)
			items, _, _, _, _ = v.snapshot()
		}
		start = items[1]
		v.show(items, 1, true)
		for i := 0; i < 200 && v.full.StatusFit(start, false, image.Pt(1, 1), false).Frame == nil; i++ {
			frame()
			time.Sleep(10 * time.Millisecond)
		}
	}
	if os.Getenv("VIEWER_TIP") != "" && edit == "" {
		target := &v.close
		if v.canMakeAvatarMain(start) {
			target = &v.avatar.main
		} else if v.canDeleteAvatar(start) {
			target = &v.avatar.remove
		}
		v.tips.over, v.tips.since, v.tips.pos = tooltipTag{target}, time.Now().Add(-time.Second), f32.Pt(float32(size.X)-250, 34)
	}
	if os.Getenv("VIEWER_AVATAR") == "delete" {
		v.avatar.asked = start
		v.avatar.modal.Open()
		for range 30 {
			frame()
			time.Sleep(10 * time.Millisecond)
		}
	}
	if edit != "" {
		// Also open what the editor shows as VIEWER_EDIT names it: see
		// viewer_edit_test.go.
		v.ffmpeg = func() string { return "" }
		renderEditor(t, v, start, edit, frame, changed)
		if os.Getenv("VIEWER_TIP") != "" {
			// The editor's Undo, at the bottom, once it is there.
			v.tips.over, v.tips.since, v.tips.pos = tooltipTag{&v.edit.undo}, time.Now().Add(-time.Second), f32.Pt(float32(size.X)/2, float32(size.Y)-40)
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
