// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/wdk"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"
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
		start := time.Unix(0, 0)
		gtx := sharedContext(new(op.Ops), image.Pt(800, 600))
		frame := func(at time.Time) bool {
			gtx.Ops.Reset()
			gtx.Source, gtx.Now = router.Source(), at
			images.BeginFrame()
			v.Layout(gtx, localization.For("en"), animate)
			images.EndFrame()
			router.Frame(gtx.Ops)
			next, requested := router.WakeupTime()
			return requested && !next.After(at)
		}
		frame(start)
		router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
		first := frame(start)
		if animate {
			if !v.open || !v.closing.requested {
				t.Fatal("Escape did not start the closing")
			}
			// Observe what the viewer itself requests, without user input
			// or other UI animations waking the window during the close.
			duration := viewerOpenFade + viewerOpenMove
			requests, frames := 0, 1
			if first {
				requests++
			}
			for n := 1; time.Duration(n)*time.Second/60 < duration; n++ {
				frames++
				if frame(start.Add(time.Duration(n) * time.Second / 60)) {
					requests++
				}
				if !v.open {
					t.Fatal("the viewer closed before the picture had gone")
				}
			}
			if requests != frames {
				t.Errorf("closing requested an immediate redraw after %d of %d frames", requests, frames)
			}
			frame(start.Add(duration))
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

func TestViewerClosingPassesInput(t *testing.T) {
	skipFrames(t)
	var images imageOps
	v := newMediaViewer(mockstore.New(time.Now(), 0), &images, func() {})
	defer v.Destroy()
	if !v.OpenProfile(2) {
		t.Fatal("the viewer did not open")
	}
	var router input.Router
	var under widget.Clickable
	var scrolling byte
	var clicks, scrolled int
	size := image.Pt(800, 600)
	gtx := sharedContext(new(op.Ops), size)
	start := time.Unix(0, 0)
	frame := func(elapsed time.Duration) {
		gtx.Ops.Reset()
		gtx.Source, gtx.Now = router.Source(), start.Add(elapsed)
		if under.Clicked(gtx) {
			clicks++
		}
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &scrolling, Kinds: pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -100, Max: 100}})
			if !ok {
				break
			}
			scrolled += int(ev.(pointer.Event).Scroll.Y)
		}
		under.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
			event.Op(gtx.Ops, &scrolling)
			return layout.Dimensions{Size: size}
		})
		images.BeginFrame()
		v.Layout(gtx, localization.For("en"), true)
		images.EndFrame()
		router.Frame(gtx.Ops)
		router.WakeupTime()
	}
	click := func(pos f32.Point) {
		router.Queue(
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos},
		)
	}
	scroll := func() {
		router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(400, 250), Scroll: f32.Pt(0, 40)})
	}
	frame(0)
	scroll()
	frame(time.Millisecond)
	if scrolled != 0 {
		t.Fatalf("scroll reached the chat while the viewer was open: %d", scrolled)
	}
	click(f32.Pt(float32(size.X-32), float32(viewerBar/2)))
	frame(2 * time.Millisecond)
	if !v.open || !v.closing.requested || clicks != 0 || router.Source().Focused(&v.keys) {
		t.Fatalf("close click did not release the viewer: closing=%v, chat clicks=%d, viewer focused=%v",
			v.closing.requested, clicks, router.Source().Focused(&v.keys))
	}
	scroll()
	click(f32.Pt(400, 250))
	frame(18 * time.Millisecond)
	if !v.open || scrolled != 40 || clicks != 1 {
		t.Fatalf("first closing frame did not release input: open=%v, scrolled=%d, clicks=%d", v.open, scrolled, clicks)
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
