// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"errors"
	"image"
	"io"
	"testing"
	"time"
)

// The demo's video, 36 seconds of 720×1280 with sound, is probed, and its
// pictures and sound are decoded from a time on, at the rate asked for.
func TestProbeFramesSound(t *testing.T) {
	if ResolveFFmpeg("") == "" || ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	const path = "../../assets/video.mp4"
	m, err := ProbeMedia(context.Background(), path, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Width != 720 || m.Height != 1280 || !m.Sound || m.Duration < 35*time.Second || m.Duration > 37*time.Second || m.FPS <= 0 {
		t.Fatalf("probed %+v", m)
	}
	f, err := NewFrames(path, "", 10*time.Second, image.Pt(90, 160), 25)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := range 50 {
		im, at, err := f.Next()
		if err != nil {
			t.Fatal(err)
		}
		if want := 10*time.Second + time.Duration(i)*40*time.Millisecond; at != want || im.Bounds().Size() != image.Pt(90, 160) {
			t.Fatalf("picture %d of %v at %v, not at %v", i, im.Bounds().Size(), at, want)
		}
		f.Release(im)
	}
	s, err := NewSound(path, "", 34*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pcm, err := io.ReadAll(s)
	if err != nil {
		t.Fatal(err)
	}
	// What is left of 36 s from 34 s, four bytes a frame.
	if seconds := float64(len(pcm)) / 4 / SoundRate; seconds < 1.5 || seconds > 2.5 {
		t.Fatalf("%.2f s of sound from 34 s", seconds)
	}
	end, err := NewFrames(path, "", 40*time.Second, image.Pt(90, 160), 25)
	if err != nil {
		t.Fatal(err)
	}
	defer end.Close()
	if _, _, err := end.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("past the end: %v", err)
	}
}
