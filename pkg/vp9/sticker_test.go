// SPDX-License-Identifier: Unlicense OR MIT

package vp9_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

const stickerPath = "../../assets/stickers/circle.webm"

func newRuntime(t *testing.T) (*vp9.Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()
	runtime, err := vp9.NewRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	return runtime, ctx
}

func readSticker(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(stickerPath)
	if err != nil {
		t.Skip("no ../../assets/stickers/circle.webm to decode")
	}
	return data
}

// TestDemuxAndDecode checks the whole path: Go demuxes the container, the
// sandbox decodes both streams, and the frames come out with transparency.
func TestDemuxAndDecode(t *testing.T) {
	runtime, ctx := newRuntime(t)
	data := readSticker(t)

	file, err := webm.Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("demuxed: %s %dx%d, %d frames, %v per frame, alpha: %v",
		file.CodecID, file.Width, file.Height, len(file.Frames), file.FrameDuration, file.HasAlpha())
	if !file.HasAlpha() {
		t.Fatal("expected the sticker to carry an alpha channel")
	}

	sticker, err := runtime.OpenSticker(ctx, "circle.webm", data)
	if err != nil {
		t.Fatal(err)
	}
	defer sticker.Close(ctx)

	var opaque, transparent int
	frame, err := sticker.FrameAt(ctx, file.FrameDuration*3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 3; i < len(frame.Pix); i += 4 {
		switch frame.Pix[i] {
		case 0:
			transparent++
		case 0xff:
			opaque++
		}
	}
	total := len(frame.Pix) / 4
	t.Logf("frame 3: %d%% transparent, %d%% opaque", transparent*100/total, opaque*100/total)
	if transparent*100/total < 30 {
		t.Errorf("expected a mostly transparent sticker, got %d%%", transparent*100/total)
	}
}

// TestMatchesFFmpeg compares the sandboxed decoder against ffmpeg's own
// libvpx-vp9 output, which is the reference these clients are measured by.
func TestMatchesFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	runtime, ctx := newRuntime(t)
	data := readSticker(t)

	reference := filepath.Join(t.TempDir(), "reference.rgba")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-c:v", "libvpx-vp9", "-i", stickerPath,
		"-frames:v", "1", "-pix_fmt", "rgba", "-f", "rawvideo", reference)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
	want, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}

	sticker, err := runtime.OpenSticker(ctx, "circle.webm", data)
	if err != nil {
		t.Fatal(err)
	}
	defer sticker.Close(ctx)
	got, err := sticker.FrameAt(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got.Pix) {
		t.Fatalf("size mismatch: ffmpeg %d bytes, decoder %d bytes", len(want), len(got.Pix))
	}

	// ffmpeg writes straight alpha; this decoder premultiplies for Gio, so the
	// colours are only comparable where the pixel is fully opaque.
	var alphaErr, colourErr float64
	var opaque int
	for i := 0; i < len(want); i += 4 {
		d := float64(want[i+3]) - float64(got.Pix[i+3])
		alphaErr += d * d
		if want[i+3] == 0xff {
			opaque++
			for c := range 3 {
				d := float64(want[i+c]) - float64(got.Pix[i+c])
				colourErr += d * d
			}
		}
	}
	pixels := len(want) / 4
	alphaRMSE := math.Sqrt(alphaErr / float64(pixels))
	colourRMSE := math.Sqrt(colourErr / float64(max(opaque, 1)*3))
	t.Logf("against ffmpeg: alpha RMSE %.2f, colour RMSE %.2f over %d opaque pixels",
		alphaRMSE, colourRMSE, opaque)

	if alphaRMSE > 2 {
		t.Errorf("alpha channel differs from ffmpeg: RMSE %.2f", alphaRMSE)
	}
	// Some colour drift is expected: ffmpeg and Go's image package do not use
	// the same YCbCr matrix.
	if colourRMSE > 24 {
		t.Errorf("colours differ from ffmpeg too much: RMSE %.2f", colourRMSE)
	}
}

// TestRejectsUntrustedInput feeds the demuxer and the decoder hostile input.
func TestRejectsUntrustedInput(t *testing.T) {
	runtime, ctx := newRuntime(t)
	data := readSticker(t)

	corrupted := make([]byte, len(data))
	copy(corrupted, data)
	for i := len(corrupted) / 2; i < len(corrupted); i++ {
		corrupted[i] ^= 0xA5
	}

	cases := map[string][]byte{
		"empty":            {},
		"garbage":          []byte("not a matroska file at all"),
		"truncated":        data[:len(data)/3],
		"corrupted frames": corrupted,
		"header only":      data[:4],
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				sticker, err := runtime.OpenSticker(ctx, name, input)
				if err != nil {
					return // rejected while demuxing, as expected
				}
				defer sticker.Close(ctx)
				for i := range sticker.FrameCount() {
					if _, err := sticker.FrameAt(ctx, time.Duration(i)*sticker.File.FrameDuration); err != nil {
						t.Logf("rejected while decoding: %v", err)
						return
					}
				}
			}()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("decoder hung on malformed input")
			}
		})
	}
}

// TestDecodeCost measures what a grid of stickers costs to decode.
func TestDecodeCost(t *testing.T) {
	runtime, ctx := newRuntime(t)
	data := readSticker(t)

	for _, count := range []int{1, 8} {
		stickers := make([]*vp9.Sticker, 0, count)
		for i := range count {
			sticker, err := runtime.OpenSticker(ctx, fmt.Sprintf("sticker%d", i), data)
			if err != nil {
				t.Fatal(err)
			}
			stickers = append(stickers, sticker)
		}

		frames := stickers[0].FrameCount()
		started := time.Now()
		for f := range frames {
			at := time.Duration(f) * stickers[0].File.FrameDuration
			for _, sticker := range stickers {
				if _, err := sticker.FrameAt(ctx, at); err != nil {
					t.Fatal(err)
				}
			}
		}
		took := time.Since(started)
		playback := time.Duration(frames) * stickers[0].File.FrameDuration
		for _, sticker := range stickers {
			sticker.Close(ctx)
		}

		t.Logf("%d stickers %dx%d: %.2f ms per frame each, %.1f%% of one core in real time",
			count, stickers[0].Size.X, stickers[0].Size.Y,
			float64(took.Microseconds())/float64(frames*count)/1000,
			100*took.Seconds()/playback.Seconds())
	}
}

// A grid calls the same decoder exports for every packet and plane. Once the
// decoder has warmed up, those calls must not allocate a new wazero stack for
// each invocation.
func TestRepeatedFramesDoNotAllocateCallStacks(t *testing.T) {
	runtimeVP9, ctx := newRuntime(t)
	sticker, err := runtimeVP9.OpenSticker(ctx, "circle.webm", readSticker(t))
	if err != nil {
		t.Fatal(err)
	}
	defer sticker.Close(ctx)
	playLoop := func() {
		for i := range sticker.FrameCount() {
			if _, err := sticker.FrameAt(ctx, time.Duration(i)*sticker.File.FrameDuration); err != nil {
				t.Fatal(err)
			}
		}
	}
	playLoop()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	playLoop()
	runtime.ReadMemStats(&after)
	bytes := after.TotalAlloc - before.TotalAlloc
	t.Logf("second loop allocated %.2f MiB", float64(bytes)/(1<<20))
	if bytes > 2<<20 {
		t.Fatalf("second loop allocated %.1f MiB; exported functions may be reopened per frame", float64(bytes)/(1<<20))
	}
}
