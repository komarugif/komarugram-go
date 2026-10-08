// SPDX-License-Identifier: Unlicense OR MIT

package player_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"komarugram/pkg/player"
	"komarugram/pkg/program"
)

// headless keeps each player from opening a window or touching the sound
// card during tests.
var headless = map[player.Kind][]string{
	player.MPV: {"--vo=null", "--ao=null", "--no-config", "--force-window=no"},
	player.VLC: {"--intf=dummy", "--vout=dummy", "--aout=dummy", "--ignore-config"},
}

// sampleVideo is PLAYER_VIDEO, or the first of ../videos/*.mp4. The seek
// test needs one longer than 20 seconds.
func sampleVideo(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("PLAYER_VIDEO"); path != "" {
		return path
	}
	paths, err := filepath.Glob("../videos/*.mp4")
	if err != nil || len(paths) == 0 {
		t.Skip("no PLAYER_VIDEO or ../videos/*.mp4 to play")
	}
	return paths[0]
}

// eachPlayer runs test for every player installed: each kind on PATH and as
// a flatpak.
func eachPlayer(t *testing.T, test func(t *testing.T, kind player.Kind, path string)) {
	for _, kind := range player.Kinds {
		// The browser is tested on its own, in TestChromium.
		if kind.Fallback() {
			continue
		}
		for _, path := range playerPaths(kind) {
			t.Run(string(kind)+strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
				test(t, kind, path)
			})
		}
	}
}

// playerPaths lists the installations of kind, found as the client finds
// them: on Windows in the App Paths too, where VLC's installer puts it.
func playerPaths(kind player.Kind) []string {
	var paths []string
	if path, err := program.LookPath(string(kind)); err == nil {
		paths = append(paths, path)
	}
	if path := program.FindFlatpak(kind.FlatpakID()); path != "" {
		paths = append(paths, path)
	}
	return paths
}

func waitFor(t *testing.T, p player.Player, name string, ok func(player.Status) bool) player.Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status := p.Status()
		if ok(status) {
			return status
		}
		if !status.Running {
			t.Fatalf("player exited while waiting for %s: %v", name, status.Err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (last status: %+v)", name, p.Status())
	return player.Status{}
}

// TestControl checks that the player reports its state and takes commands.
func TestControl(t *testing.T) {
	eachPlayer(t, testControl)
}

func testControl(t *testing.T, kind player.Kind, path string) {
	ctx := context.Background()

	p, err := player.Open(ctx, kind, path, sampleVideo(t), headless[kind]...)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	status := waitFor(t, p, "duration", func(s player.Status) bool { return s.Duration > 0 })
	t.Logf("%s reports duration %v", kind, status.Duration.Round(time.Millisecond))

	waitFor(t, p, "playback start", func(s player.Status) bool { return s.Position > 0 })

	if err := p.Seek(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	seeked := waitFor(t, p, "seek", func(s player.Status) bool { return s.Position > 9*time.Second })
	t.Logf("after seeking forward: position %v", seeked.Position.Round(time.Millisecond))

	if err := p.TogglePause(); err != nil {
		t.Fatal(err)
	}
	paused := waitFor(t, p, "pause", func(s player.Status) bool { return s.Paused })
	t.Logf("paused at %v", paused.Position.Round(time.Millisecond))

	// A seek lands on a keyframe, and VLC reports the time it aimed at
	// before the one it reached: let the position settle first.
	time.Sleep(time.Second)
	before := p.Status().Position
	time.Sleep(time.Second)
	if after := p.Status().Position; after != before {
		t.Errorf("position moved while paused: %v -> %v", before, after)
	}
}

// throttled makes reads slow enough that the player cannot simply swallow the
// whole file, the way a download over a real network behaves.
type throttled struct {
	inner          io.ReaderAt
	bytesPerSecond int
}

func (t throttled) ReadAt(p []byte, off int64) (int, error) {
	n, err := t.inner.ReadAt(p, off)
	if n > 0 {
		time.Sleep(time.Duration(float64(n) / float64(t.bytesPerSecond) * float64(time.Second)))
	}
	return n, err
}

// TestStreamSeek plays a file that is only reachable over loopback and checks
// that seeking makes the player come back for another range.
func TestStreamSeek(t *testing.T) {
	eachPlayer(t, testStreamSeek)
}

func testStreamSeek(t *testing.T, kind player.Kind, path string) {
	video := sampleVideo(t)
	file, err := os.Open(video)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}

	// 300 KB/s: fast enough to play, too slow to prefetch the whole file.
	stream, err := player.Serve("assets/video.mp4", throttled{inner: file, bytesPerSecond: 300 << 10}, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	t.Logf("serving %s (%d KB) at %s", filepath.Base(video), info.Size()/1024, stream.URL())

	p, err := player.Open(context.Background(), kind, path, stream.URL(), headless[kind]...)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	waitFor(t, p, "playback over http", func(s player.Status) bool { return s.Position > 0 })
	initial := stream.Requests()

	if err := p.Seek(20 * time.Second); err != nil {
		t.Fatal(err)
	}
	forward := waitFor(t, p, "seek over http", func(s player.Status) bool { return s.Position > 19*time.Second })
	t.Logf("seeked forward to %v after %d request(s)", forward.Position.Round(time.Millisecond), stream.Requests())

	// Seeking backwards is the case a player cannot serve by reading on: it
	// has to ask the source for an earlier range.
	if err := p.Seek(-18 * time.Second); err != nil {
		t.Fatal(err)
	}
	back := waitFor(t, p, "seek back", func(s player.Status) bool { return s.Position < 10*time.Second })
	t.Logf("seeked back to %v; %d request(s) in total, %d before any seek",
		back.Position.Round(time.Millisecond), stream.Requests(), initial)

	if stream.Requests() <= initial {
		t.Error("expected seeking to fetch another range from the source")
	}
}
