// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// turnPicture turns clockwise: the first row of a picture becomes its last column.
func TestTurnPicture(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i, v := range []uint8{1, 2, 3, 4, 5, 6} {
		src.Pix[i*4] = v
	}
	for _, c := range []struct {
		turn int
		size image.Point
		want []uint8
	}{
		{90, image.Pt(2, 3), []uint8{4, 1, 5, 2, 6, 3}},
		{180, image.Pt(3, 2), []uint8{6, 5, 4, 3, 2, 1}},
		{270, image.Pt(2, 3), []uint8{3, 6, 2, 5, 1, 4}},
	} {
		dst := image.NewRGBA(image.Rectangle{Max: c.size})
		turnPicture(dst, src, c.turn)
		for i, v := range c.want {
			if dst.Pix[i*4] != v {
				t.Errorf("turn %d, pixel %d: %d, not %d", c.turn, i, dst.Pix[i*4], v)
			}
		}
	}
}

// A video a phone recorded turned plays turned in the sandbox: red over blue, stored,
// shows blue left of red, of the shape it shows at.
func TestSandboxTurnsTheVideo(t *testing.T) {
	roundDecoders(t)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	plain, turned := filepath.Join(dir, "plain.mp4"), filepath.Join(dir, "turned.mp4")
	source := "color=red:size=160x90:rate=25:duration=1[a];color=blue:size=160x90:rate=25:duration=1[b];[a][b]vstack,format=yuv420p"
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", source, "-c:v", "libx264", "-preset", "ultrafast", plain).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-display_rotation", "-90", "-i", plain, "-c", "copy", turned).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	data, err := os.ReadFile(turned)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	round, err := openVideoFile(ctx, webmFile(data))
	if err != nil {
		t.Fatal(err)
	}
	defer round.close()
	if round.turn != 90 {
		t.Fatalf("turn %d", round.turn)
	}
	round.takeIn(time.Now(), image.Pt(800, 800))
	start := time.Now()
	shown := make(chan struct{}, 8)
	go round.run(ctx, func() time.Duration { return time.Since(start) }, func() {
		select {
		case shown <- struct{}{}:
		default:
		}
	})
	select {
	case <-shown:
	case <-ctx.Done():
		t.Fatal("no picture")
	}
	picture, ok := round.takeIn(time.Now(), image.Pt(800, 800)).(*image.RGBA)
	if !ok || picture.Bounds().Size() != image.Pt(180, 160) {
		t.Fatalf("%v", picture.Bounds())
	}
	left, right := picture.RGBAAt(45, 80), picture.RGBAAt(135, 80)
	if left.B < 150 || left.R > 100 || right.R < 150 || right.B > 100 {
		t.Fatalf("left %v, right %v", left, right)
	}
}
