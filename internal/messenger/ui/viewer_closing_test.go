// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

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
