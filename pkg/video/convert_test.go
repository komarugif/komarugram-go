// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Each format comes out an MP4 with its movie box first, in H.264, of the length and
// size it had, with its sound.
func TestConvertToMP4(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if ResolveFFprobe("") == "" {
		t.Skip("ffprobe unavailable")
	}
	input := []string{"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=2", "-f", "lavfi", "-i", "sine=frequency=440:duration=2"}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"vp9.webm", []string{"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-c:a", "libopus"}},
		{"h264.mkv", []string{"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac"}},
		{"old.avi", []string{"-c:v", "mpeg4", "-c:a", "mp3"}},
		{"theora.ogv", []string{"-c:v", "libtheora", "-c:a", "libvorbis"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			in, out := filepath.Join(dir, c.name), filepath.Join(dir, "out.mp4")
			args := append([]string{"-hide_banner", "-loglevel", "error", "-y"}, input...)
			if data, err := exec.Command(ffmpeg, append(append(args, c.args...), in)...).CombinedOutput(); err != nil {
				t.Skipf("this FFmpeg cannot: %v: %s", err, data)
			}
			shares := 0
			last := -1.
			err := ConvertToMP4(context.Background(), "", in, out, func(p GIFProgress) {
				shares++
				last = p.Share()
			})
			if err != nil {
				t.Fatal(err)
			}
			if shares == 0 || last <= 0 {
				t.Errorf("%d reports, the last %v", shares, last)
			}
			got, err := ReadContainer(out)
			if err != nil {
				t.Fatal(err)
			}
			if got.Width != 320 || got.Height != 180 || !got.Streaming || !got.Sound || got.Duration < 1800*time.Millisecond || got.Duration > 2200*time.Millisecond {
				t.Fatalf("%+v", got)
			}
			probed, err := ProbeMedia(context.Background(), out, "")
			if err != nil {
				t.Fatal(err)
			}
			if probed.AudioCodec != "aac" {
				t.Errorf("sound in %s", probed.AudioCodec)
			}
		})
	}
}

// A conversion stopped leaves nothing behind, and says so; what is no video is
// told as an error and makes no file.
func TestConvertToMP4Stops(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	in, out := filepath.Join(dir, "long.webm"), filepath.Join(dir, "out.mp4")
	if data, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20", "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", in).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, data)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = ConvertToMP4(ctx, "", in, out, func(GIFProgress) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("what was stopped stayed")
	}
	junk := filepath.Join(dir, "junk.webm")
	if err := os.WriteFile(junk, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConvertToMP4(context.Background(), "", junk, out, func(GIFProgress) {}); err == nil {
		t.Fatal("converted junk")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("junk made a file")
	}
}
