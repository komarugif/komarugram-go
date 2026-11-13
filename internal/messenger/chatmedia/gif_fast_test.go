// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"context"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/h264"
)

// A GIF of 60 pictures a second plays in the sandbox at its own speed, on
// the 30 ticks a second GIFs show pictures on: the pictures between the
// ticks are decoded, and not shown.
func TestFastGIFPlaysAtItsSpeedInSandbox(t *testing.T) {
	// The module is not in this repository: KOMARUGRAM_AVCDEC names a
	// build of it.
	module := os.Getenv("KOMARUGRAM_AVCDEC")
	if module == "" {
		t.Skip("set KOMARUGRAM_AVCDEC to an avcdec.wasm")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg to make the GIF with")
	}
	path := filepath.Join(t.TempDir(), "fast.mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=60:duration=20",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg did not make the GIF: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(&fakeSource{data: data}, func() {})
	defer m.Close()
	m.avc.open = func(ctx context.Context) (*h264.Runtime, error) {
		b, err := os.ReadFile(module)
		if err != nil {
			return nil, err
		}
		return h264.NewRuntime(ctx, b)
	}
	m.ConfigureDecoders(false, true, "")
	msg := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "fast", MIMEType: "video/mp4", Duration: 20 * time.Second}}
	draw := func() image.Image {
		m.BeginFrame()
		status := m.StatusFit(msg, true, image.Pt(100, 100), true)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		return status.Frame
	}
	for end := time.Now().Add(10 * time.Second); m.GIFPosition(msg) == 0 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		draw()
	}
	from, started := m.GIFPosition(msg), time.Now()
	if from == 0 {
		t.Fatal("the GIF did not play")
	}
	pictures := map[image.Image]bool{}
	var last image.Image
	shown := 0
	for time.Since(started) < 3*time.Second {
		if im := draw(); im != nil && im != last {
			last = im
			shown++
			pictures[im] = true
		}
		time.Sleep(2 * time.Millisecond)
	}
	played, took := m.GIFPosition(msg)-from, time.Since(started)
	// Shown a tick each, the pictures played half of it; a busy machine
	// plays a little less than all.
	if played < took*65/100 || played > took*12/10 {
		t.Fatalf("played %v of the GIF in %v", played, took)
	}
	if perSecond := float64(shown) / took.Seconds(); perSecond > 33 {
		t.Fatalf("%.0f pictures a second shown, on 30 ticks", perSecond)
	}
}

// A GIF is decoded without the deblocking filter where it is shown four
// times smaller or more, in the tiles of the panel, and not in a message.
func TestGIFDecodedRoughlyInTilesOnly(t *testing.T) {
	for _, c := range []struct {
		source, size image.Point
		want         bool
	}{
		{image.Pt(1280, 720), image.Pt(178, 100), true},
		{image.Pt(720, 1280), image.Pt(100, 178), true},
		{image.Pt(1280, 720), image.Pt(320, 180), true},
		{image.Pt(1280, 720), image.Pt(420, 236), false},
		{image.Pt(480, 270), image.Pt(178, 100), false},
		{image.Pt(320, 240), image.Pt(320, 240), false},
		{image.Pt(1280, 720), image.Point{}, false},
	} {
		if got := roughlyDecoded(c.source, c.size); got != c.want {
			t.Errorf("a GIF of %v shown in %v: decoded roughly %v, want %v", c.source, c.size, got, c.want)
		}
	}
}
