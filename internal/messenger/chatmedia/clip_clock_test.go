// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"image"
	"testing"
	"time"
)

// A loop played on the UI's clock goes on when the wall clock is set
// back, as a time sync sets it: the frame's time comes before the loop's
// start then.
func TestClipFrameAfterClockGoesBack(t *testing.T) {
	m := &Manager{}
	frames := make([]*image.RGBA, 3)
	for i := range frames {
		frames[i] = image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	e := &entry{animate: true, clip: &stickerClip{frames: frames, frameDuration: 40 * time.Millisecond}}
	start := time.Date(2026, 10, 26, 0, 34, 0, 0, time.UTC)
	m.now = start
	if m.clipFrame(e) == nil {
		t.Fatal("no picture of a loop that plays")
	}
	m.now = start.Add(100 * time.Millisecond)
	m.clipFrame(e)
	m.now = start.Add(-10 * time.Second)
	if m.clipFrame(e) == nil {
		t.Fatal("no picture after the clock went back")
	}
	if e.clipAt < 0 {
		t.Fatalf("the loop is at %v", e.clipAt)
	}
	// It goes on from there as the clock goes on.
	m.now = m.now.Add(80 * time.Millisecond)
	if m.clipFrame(e) == nil || e.clipAt < 0 {
		t.Fatalf("the loop stopped at %v", e.clipAt)
	}
}
