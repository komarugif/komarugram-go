// SPDX-License-Identifier: Unlicense OR MIT

package player_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/pkg/miniapp"
	"komarugram/pkg/player"
)

// chromiumHeadless keeps the browser from opening a window or making a
// sound during tests.
var chromiumHeadless = []string{"--headless=new", "--mute-audio"}

// chromiumVideo is PLAYER_VIDEO, the first of ../videos/*.mp4, or else one
// of the stickers in ../../assets: the browser plays WebM whatever its build.
func chromiumVideo(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("PLAYER_VIDEO"); path != "" {
		return path
	}
	for _, pattern := range []string{"../videos/*.mp4", "../../assets/rigby/*.webm"} {
		if paths, _ := filepath.Glob(pattern); len(paths) > 0 {
			return paths[0]
		}
	}
	t.Skip("no PLAYER_VIDEO, ../videos/*.mp4 or ../../assets/rigby/*.webm to play")
	return ""
}

// serveFile serves the file at path the way the messenger serves a video.
func serveFile(t *testing.T, path string) *player.Stream {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := player.Serve("video", file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Close() })
	return stream
}

// TestChromium plays a video in the browser of Mini Apps, KOMARUGRAM_MINIAPP_BROWSER
// or the one found, and checks that it reports its state and takes commands.
func TestChromium(t *testing.T) {
	if !miniapp.Available() {
		t.Skip("no Chromium-based browser")
	}
	stream := serveFile(t, chromiumVideo(t))
	p, err := player.Open(context.Background(), player.Chromium, "", stream.URL(), chromiumHeadless...)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	status := waitFor(t, p, "duration", func(s player.Status) bool { return s.Duration > 0 || s.Err != nil })
	if status.Err != nil {
		t.Fatal(status.Err)
	}
	t.Logf("duration %v", status.Duration.Round(time.Millisecond))
	waitFor(t, p, "playback start", func(s player.Status) bool { return s.Position > 0 })
	if stream.Requests() == 0 {
		t.Error("the browser never asked the stream for the file")
	}

	if err := p.TogglePause(); err != nil {
		t.Fatal(err)
	}
	paused := waitFor(t, p, "pause", func(s player.Status) bool { return s.Paused })
	if err := p.Seek(-time.Hour); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, "seek to the start", func(s player.Status) bool { return s.Position == 0 })
	t.Logf("paused at %v, then seeked to the start", paused.Position.Round(time.Millisecond))

	start := time.Now()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("closed in %v", time.Since(start).Round(time.Millisecond))
	if p.Status().Running {
		t.Error("the player still runs after Close")
	}
}

// TestChromiumCannotPlay checks that a file the browser cannot play is
// reported as such, rather than leaving a black window.
func TestChromiumCannotPlay(t *testing.T) {
	if !miniapp.Available() {
		t.Skip("no Chromium-based browser")
	}
	path := filepath.Join(t.TempDir(), "noise.mp4")
	if err := os.WriteFile(path, make([]byte, 64<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	stream := serveFile(t, path)
	p, err := player.Open(context.Background(), player.Chromium, "", stream.URL(), chromiumHeadless...)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	status := waitFor(t, p, "an error", func(s player.Status) bool { return s.Err != nil })
	if stream.Requests() == 0 {
		t.Error("the browser never asked the stream for the file")
	}
	if !errors.Is(status.Err, player.ErrCannotPlay) {
		t.Errorf("got %v, want ErrCannotPlay", status.Err)
	}
	t.Log(status.Err)
}

// TestChromiumStartsAt checks that the browser starts the video at the
// time StartArgs names, as a timestamp opens it, and moves it to another.
func TestChromiumStartsAt(t *testing.T) {
	if !miniapp.Available() {
		t.Skip("no Chromium-based browser")
	}
	stream := serveFile(t, chromiumVideo(t))
	start := 1500 * time.Millisecond
	extra := append(append([]string{}, chromiumHeadless...), player.Chromium.StartArgs(start)...)
	p, err := player.Open(context.Background(), player.Chromium, "", stream.URL(), extra...)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	status := waitFor(t, p, "duration", func(s player.Status) bool { return s.Duration > 0 || s.Err != nil })
	if status.Err != nil {
		t.Fatal(status.Err)
	}
	if status.Duration < 2*time.Second {
		t.Skipf("the video of %v is too short", status.Duration)
	}
	// The first position told is that of a video that played from the
	// start for a poll or two at most, unless it started at start, or
	// near it: a WebM of no index, as the stickers are, seeks only to
	// where its clusters begin.
	first := waitFor(t, p, "playback start", func(s player.Status) bool { return s.Position > 0 })
	if first.Position < start-500*time.Millisecond {
		t.Fatalf("it started at %v, not %v", first.Position, start)
	}
	if err := p.TogglePause(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, "pause", func(s player.Status) bool { return s.Paused })
	if err := p.SeekTo(500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, "the seek", func(s player.Status) bool { return s.Position <= time.Second })
}
