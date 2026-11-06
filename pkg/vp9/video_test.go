// SPDX-License-Identifier: Unlicense OR MIT

package vp9_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

// clip makes a WebM with FFmpeg: size pictures of VP9 with keyframes every 25, and
// sound in Opus.
func clip(t *testing.T, codec string) []byte {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "clip.webm")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-c:v", codec, "-g", "25", "-deadline", "realtime", "-cpu-used", "8", "-c:a", "libopus", path}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A video plays from its first picture to its last and on from the first, moves to
// the keyframe before a time, and gives pictures that move.
func TestVideoPlaysAndSeeks(t *testing.T) {
	data := clip(t, "libvpx-vp9")
	index, err := webm.ReadIndex(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rt, err := vp9.NewRuntimeWithLimits(ctx, vp9.VideoLimits)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	v, err := rt.OpenFile(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(ctx)
	if v.Size() != image.Pt(320, 180) || v.Pictures() != 100 || v.Duration() < 3900*time.Millisecond || v.Duration() > 4100*time.Millisecond {
		t.Fatalf("%v, %d pictures, %v", v.Size(), v.Pictures(), v.Duration())
	}
	var first, later []byte
	for i := range v.Pictures() {
		got, err := v.Next(ctx)
		if err != nil {
			t.Fatalf("picture %d: %v", i, err)
		}
		if got != i {
			t.Fatalf("picture %d told as %d", i, got)
		}
		switch i {
		case 0:
			first = bytes.Clone(v.Picture(image.Pt(320, 180)).Pix)
		case 30:
			later = bytes.Clone(v.Picture(image.Pt(320, 180)).Pix)
		}
	}
	if bytes.Equal(first, later) {
		t.Fatal("the picture does not change")
	}
	k, err := v.Seek(ctx, 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if k != 25 {
		t.Fatalf("the keyframe before 1.5 s is %d", k)
	}
	if i, err := v.Next(ctx); err != nil || i != 25 {
		t.Fatalf("%d: %v", i, err)
	}
	for range 5 {
		if _, err := v.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// From a keyframe it decodes to the same picture as from the start.
	if again := v.Picture(image.Pt(320, 180)).Pix; !bytes.Equal(again, later) {
		t.Fatal("after the move, picture 30 differs")
	}
	// The last picture, then the first again.
	if _, err := v.Seek(ctx, 4*time.Second); err != nil {
		t.Fatal(err)
	}
	for range v.Pictures() {
		if _, err := v.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// VP8 is told apart: the module has only VP9, and a player has somewhere else to go.
func TestVideoOtherCodec(t *testing.T) {
	data := clip(t, "libvpx")
	index, err := webm.ReadIndex(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rt, err := vp9.NewRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	if _, err := rt.OpenFile(ctx, index); !errors.Is(err, vp9.ErrCodec) {
		t.Fatal(err)
	}
}
