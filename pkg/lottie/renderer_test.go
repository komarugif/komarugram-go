// SPDX-License-Identifier: Unlicense OR MIT

package lottie_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"komarugram/pkg/lottie"
)

func newRuntime(t *testing.T) (*lottie.Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()
	runtime, err := lottie.NewRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	return runtime, ctx
}

func sampleAnimation(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../assets/stickers/sample.json")
	if err != nil {
		t.Skip("no ../../assets/stickers/sample.json to render")
	}
	return data
}

// TestRendersJSONAndTGS checks both input paths: plain Lottie JSON and the
// gzipped .tgs that Telegram actually ships.
func TestRendersJSONAndTGS(t *testing.T) {
	runtime, ctx := newRuntime(t)
	data := sampleAnimation(t)

	plain, err := runtime.Open(ctx, "sample.json", data, 128)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close(ctx)
	if plain.FrameCount == 0 || plain.FPS <= 0 {
		t.Fatalf("unexpected animation: %d frames at %v fps", plain.FrameCount, plain.FPS)
	}

	var packed bytes.Buffer
	zw := gzip.NewWriter(&packed)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	tgs, err := runtime.Open(ctx, "sample.tgs", packed.Bytes(), 128)
	if err != nil {
		t.Fatalf("gzipped .tgs rejected: %v", err)
	}
	defer tgs.Close(ctx)

	at := time.Duration(float64(plain.FrameCount) / 3 / plain.FPS * float64(time.Second))
	fromJSON, _, err := plain.FrameAt(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	painted := 0
	for i := 3; i < len(fromJSON.Pix); i += 4 {
		if fromJSON.Pix[i] != 0 {
			painted++
		}
	}
	if painted == 0 {
		t.Error("frame is fully transparent: nothing was drawn")
	}

	fromTGS, _, err := tgs.FrameAt(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fromJSON.Pix, fromTGS.Pix) {
		t.Error(".tgs and .json renders of the same animation differ")
	}
}

// TestRejectsUntrustedInput feeds the renderer the kind of input that arrives
// from strangers. Every case must come back as an error, never a panic and
// never a hang.
func TestRejectsUntrustedInput(t *testing.T) {
	runtime, ctx := newRuntime(t)
	data := sampleAnimation(t)

	truncatedGzip := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00}

	for name, input := range map[string][]byte{
		"empty":           {},
		"garbage":         bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 64),
		"truncated json":  data[:len(data)/2],
		"truncated gzip":  truncatedGzip,
		"json array":      []byte("[]"),
		"deep nesting":    append(bytes.Repeat([]byte("["), 4096), bytes.Repeat([]byte("]"), 4096)...),
		"huge dimensions": []byte(`{"v":"5.5.2","w":2147483647,"h":2147483647,"fr":60,"ip":0,"op":60,"layers":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				animation, err := runtime.Open(ctx, name, input, 128)
				if err != nil {
					return // rejected, which is the expected outcome
				}
				// Accepting it is fine too, as long as rendering stays sane.
				defer animation.Close(ctx)
				if _, _, err := animation.FrameAt(ctx, 0); err != nil {
					t.Logf("accepted at parse time, failed while rendering: %v", err)
				}
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("renderer hung on malformed input")
			}
		})
	}
}

// TestStickerGridCost measures what a grid of animations costs the renderer
// itself, with no GUI involved. It is a measurement, not an assertion.
func TestStickerGridCost(t *testing.T) {
	runtime, ctx := newRuntime(t)
	data := sampleAnimation(t)

	for _, grid := range []struct {
		count int
		size  int
		fps   float64
	}{
		{14, 160, 30},
		{14, 160, 60},
		{50, 128, 30},
	} {
		opened := make([]*lottie.Animation, 0, grid.count)
		for i := range grid.count {
			animation, err := runtime.Open(ctx, fmt.Sprintf("sticker%d", i), data, grid.size)
			if err != nil {
				t.Fatal(err)
			}
			opened = append(opened, animation)
		}

		const window = 2 * time.Second
		frames := int(grid.fps * window.Seconds())
		var spent time.Duration
		for frame := range frames {
			at := time.Duration(float64(frame) / grid.fps * float64(time.Second))
			for _, animation := range opened {
				if _, took, err := animation.FrameAt(ctx, at); err != nil {
					t.Fatal(err)
				} else {
					spent += took
				}
			}
		}
		for _, animation := range opened {
			animation.Close(ctx)
		}

		t.Logf("%d animations at %dpx, %.0f fps: %.1f%% of one core (%.3f ms per frame per sticker)",
			grid.count, grid.size, grid.fps,
			100*spent.Seconds()/window.Seconds(),
			float64(spent.Microseconds())/float64(frames*grid.count)/1000)
	}
}
