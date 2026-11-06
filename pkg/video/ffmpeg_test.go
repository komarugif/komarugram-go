// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"komarugram/pkg/program"
)

func TestCustomFFmpegPlayer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture needs Unix")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip(err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "custom ffmpeg")
	if err := os.Symlink(ffmpeg, path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ffprobe, filepath.Join(dir, "ffprobe")); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "clip.mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=16x16:d=0.2", "-c:v", "mpeg4", media).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	t.Setenv("PATH", dir)
	if ResolveFFmpeg("") != "" {
		t.Fatal("unexpected system FFmpeg")
	}
	if got := ResolveFFmpeg(path); got != path {
		t.Fatalf("custom path: %q", got)
	}
	if _, err := CheckFFmpeg(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayerWithFFmpeg(media, 32, path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	p.Start()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		im, err := p.Frame()
		if err != nil {
			t.Fatal(err)
		}
		if im != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("custom FFmpeg did not produce a frame")
}

// A video recorded turned is told at the size its pictures come out at, as
// FFmpeg turns them: the scaler would squeeze them otherwise.
func TestProbeMediaTellsTheTurnedSize(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	stored, turned := filepath.Join(dir, "stored.mp4"), filepath.Join(dir, "turned.mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=s=64x36:d=0.2", stored).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	if out, err := exec.Command(ffmpeg, "-v", "error", "-display_rotation", "90", "-i", stored, "-c", "copy", turned).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot turn a video: %v %s", err, out)
	}
	m, err := ProbeMedia(context.Background(), stored, ffmpeg)
	if err != nil || m.Width != 64 || m.Height != 36 {
		t.Fatalf("stored: %+v, %v", m, err)
	}
	m, err = ProbeMedia(context.Background(), turned, ffmpeg)
	if err != nil || m.Width != 36 || m.Height != 64 {
		t.Fatalf("turned: %+v, %v", m, err)
	}
}

// With searching off, only the FFmpeg the user set is found, and the
// ffprobe beside it.
func TestResolveWithSearchingOff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake programs are shell scripts")
	}
	dir := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	program.SetSearching(false)
	t.Cleanup(func() { program.SetSearching(true) })
	if got := ResolveFFmpeg(""); got != "" {
		t.Fatalf("found %q with searching off", got)
	}
	if got := ResolveFFprobe(""); got != "" {
		t.Fatalf("found ffprobe %q with searching off", got)
	}
	custom := filepath.Join(dir, "ffmpeg")
	if got := ResolveFFmpeg(custom); got != custom {
		t.Fatalf("custom FFmpeg resolved to %q", got)
	}
	if got := ResolveFFprobe(custom); got != filepath.Join(dir, "ffprobe") {
		t.Fatalf("ffprobe beside the custom FFmpeg resolved to %q", got)
	}
	program.SetSearching(true)
	if got := ResolveFFmpeg(""); got != custom {
		t.Fatalf("found %q on PATH", got)
	}
}
