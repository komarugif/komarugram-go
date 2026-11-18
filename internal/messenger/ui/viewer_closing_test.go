// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/wdk"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/op"
)

// Escape starts closing the viewer, which stays open while the controls and
// then the picture fade out, and is closed when they have; with animations
// off it closes at once.
func TestViewerClosesWithAnimation(t *testing.T) {
	skipFrames(t)
	for _, animate := range []bool{true, false} {
		var images imageOps
		v := newMediaViewer(mockstore.New(time.Now(), 0), &images, func() {})
		if !v.OpenProfile(2) {
			t.Fatal("the viewer did not open")
		}
		var router input.Router
		now := time.Unix(0, 0)
		frame := func() {
			gtx := sharedContext(new(op.Ops), image.Pt(800, 600))
			gtx.Source, gtx.Now = router.Source(), now
			images.BeginFrame()
			v.Layout(gtx, localization.For("en"), animate)
			images.EndFrame()
		}
		frame()
		router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
		frame()
		if animate {
			if !v.open || !v.closing.requested {
				t.Fatal("Escape did not start the closing")
			}
			now = now.Add(viewerOpenFade + viewerOpenMove/2)
			frame()
			if !v.open {
				t.Fatal("the viewer closed before the picture had gone")
			}
			now = now.Add(viewerOpenMove)
			frame()
		}
		if v.open {
			t.Fatalf("animate %v: the viewer stayed open", animate)
		}
		if v.closing.requested {
			t.Fatal("a closed viewer is still closing")
		}
		v.Destroy()
	}
}

func TestViewerClosingAnimationKinds(t *testing.T) {
	gtx := sharedContext(new(op.Ops), image.Pt(800, 600))
	gtx.Now = time.Unix(0, 0)

	wdk.SetDisabledAnimations(gtx, wdk.AnimationViewerAttachment)
	chromeOnly := viewerClosing{requested: true}
	if chrome, gone, done := chromeOnly.step(gtx, true); chrome != 1 || gone != 0 || done {
		t.Fatalf("controls should start fading before the viewer closes: %v, %v, %v", chrome, gone, done)
	}
	gtx.Now = gtx.Now.Add(viewerOpenFade)
	if _, _, done := chromeOnly.step(gtx, true); !done {
		t.Fatal("the viewer stayed open after the fade with attachment motion disabled")
	}

	wdk.SetDisabledAnimations(gtx, wdk.AnimationViewer)
	attachmentOnly := viewerClosing{requested: true}
	if chrome, _, done := attachmentOnly.step(gtx, true); chrome != 0 || done {
		t.Fatalf("controls should disappear immediately while the attachment animates: %v, %v", chrome, done)
	}
	gtx.Now = gtx.Now.Add(viewerOpenMove / 2)
	if _, gone, done := attachmentOnly.step(gtx, true); gone <= 0 || gone >= 1 || done {
		t.Fatalf("attachment should be animating without waiting for the controls: %v, %v", gone, done)
	}
}

// A viewer in a window of its own, whose window closes with it, closes at
// once.
func TestStandaloneViewerClosesAtOnce(t *testing.T) {
	v := newMediaViewer(mockstore.New(time.Now(), 0), new(imageOps), func() {})
	defer v.Destroy()
	v.standalone = true
	if !v.OpenProfile(2) {
		t.Fatal("the viewer did not open")
	}
	v.opening = viewerOpening{}
	v.dismiss()
	if v.open {
		t.Fatal("the viewer of a window stayed open")
	}
}
