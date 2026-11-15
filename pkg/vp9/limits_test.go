// SPDX-License-Identifier: Unlicense OR MIT

package vp9_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"komarugram/pkg/sandbox"
	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"

	"komarugram/internal/testenv"
)

// bigFrame encodes one 4096x4096 VP9 frame: a legal bitstream, eight times
// wider than any sticker, which libvpx needs about 50 MiB to decode.
func bigFrame(t *testing.T) (*webm.File, []byte) {
	t.Helper()
	testenv.Integration(t, "runs FFmpeg")
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "big.webm")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=4096x4096:r=30:d=0.1", "-frames:v", "1",
		"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := webm.Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	return file, data
}

func runtimeWith(t *testing.T, limits vp9.Limits) (*vp9.Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()
	runtime, err := vp9.NewRuntimeWithLimits(ctx, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	return runtime, ctx
}

// TestFrameSizeLimit checks that a frame larger than MaxSide is refused before
// the host allocates anything for it, whichever of container and bitstream
// states the size.
func TestFrameSizeLimit(t *testing.T) {
	file, data := bigFrame(t)

	runtime, ctx := newRuntime(t)
	if _, err := runtime.OpenSticker(ctx, "big.webm", data); err == nil {
		t.Fatal("a 4096x4096 container was accepted")
	} else {
		t.Logf("container: %v", err)
	}

	// Give the sandbox room to decode it, so that the check on the host side
	// is what refuses the frame.
	roomy := vp9.DefaultLimits
	roomy.Memory = 256 << 20
	runtime, ctx = runtimeWith(t, roomy)
	decoder, err := runtime.NewDecoder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close(ctx)
	if _, err := decoder.Decode(ctx, file.Frames[0].Data); err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("bitstream frame of 4096x4096: got %v, want the size limit", err)
	} else {
		t.Logf("bitstream: %v", err)
	}
}

// TestMemoryLimit checks that a decoder which needs more memory than its
// sandbox may have fails with an error, and that the runtime stays usable.
func TestMemoryLimit(t *testing.T) {
	file, _ := bigFrame(t)
	limits := vp9.DefaultLimits
	limits.Memory = 16 << 20
	limits.MaxSide = 4096
	runtime, ctx := runtimeWith(t, limits)

	decoder, err := runtime.NewDecoder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = decoder.Decode(ctx, file.Frames[0].Data)
	decoder.Close(ctx)
	if err == nil {
		t.Fatal("a 4096x4096 frame decoded within 16 MiB")
	}
	t.Logf("over the memory limit: %v", err)

	// The failure belonged to that decoder alone.
	sticker, err := runtime.OpenSticker(ctx, "circle.webm", readSticker(t))
	if err != nil {
		t.Fatal(err)
	}
	defer sticker.Close(ctx)
	if _, err := sticker.FrameAt(ctx, 0); err != nil {
		t.Fatalf("runtime unusable after a decoder ran out of memory: %v", err)
	}
	if used := runtime.Budget().Used(); used > 16<<20 {
		t.Errorf("budget still holds %d bytes of the failed decoder", used)
	}
}

// TestBudget checks that decoders sharing a budget are refused once it is
// spent, and that closing them gives all of it back.
func TestBudget(t *testing.T) {
	data := readSticker(t)
	limits := vp9.DefaultLimits
	limits.Budget = sandbox.NewBudget(8 << 20)
	runtime, ctx := runtimeWith(t, limits)

	var opened []*vp9.Sticker
	var refused error
	for i := range 64 {
		sticker, err := runtime.OpenSticker(ctx, "circle.webm", data)
		if err == nil {
			_, err = sticker.FrameAt(ctx, time.Duration(i)*sticker.File.FrameDuration)
			opened = append(opened, sticker)
		}
		if err != nil {
			refused = err
			break
		}
	}
	if refused == nil {
		t.Fatal("64 stickers fit in an 8 MiB budget")
	}
	// Either a new sandbox did not fit (ErrBudget) or a running one could not
	// grow, which libvpx reports by trapping; both are errors, not crashes.
	t.Logf("%d stickers fit, then: %v", len(opened), refused)
	if used := limits.Budget.Used(); used > 8<<20 {
		t.Errorf("budget overdrawn: %d bytes", used)
	}
	for _, sticker := range opened {
		sticker.Close(ctx)
	}
	if used := limits.Budget.Used(); used != 0 {
		t.Errorf("closed stickers still hold %d bytes of the budget", used)
	}
}

// TestSlowLimit checks that an operation overrunning the time limit fails and
// closes its sandbox, so that later frames fail at once.
func TestSlowLimit(t *testing.T) {
	data := readSticker(t)
	limits := vp9.DefaultLimits
	limits.Slow = time.Microsecond
	runtime, ctx := runtimeWith(t, limits)

	started := time.Now()
	sticker, err := runtime.OpenSticker(ctx, "circle.webm", data)
	if err == nil {
		defer sticker.Close(ctx)
		_, err = sticker.FrameAt(ctx, 0)
	}
	if err == nil {
		t.Fatal("decoding finished within a microsecond")
	}
	t.Logf("after %v: %v", time.Since(started), err)
}

// A decode over the time limit, as one on a slow machine under other work,
// is made once more in new decoders, and the sticker shows its frame: on
// Haiku in a virtual machine a sticker's first frame took 300 to 600 ms and
// the sticker was left blank. One that takes too long again fails, after
// the one retry.
func TestStickerDecodesAgainAfterASlowOperation(t *testing.T) {
	ctx := context.Background()
	runtime, err := vp9.NewRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(ctx)
	t.Cleanup(func() { vp9.SetSlowFault(nil) })
	for _, c := range []struct {
		name   string
		slow   int // how many decodes are too slow
		failed bool
	}{
		{"once", 1, false},
		{"always", 1 << 20, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			sticker, err := runtime.OpenSticker(ctx, "circle.webm", readSticker(t))
			if err != nil {
				t.Fatal(err)
			}
			defer sticker.Close(ctx)
			slow := 0
			vp9.SetSlowFault(func() bool {
				slow++
				return slow <= c.slow
			})
			frame, err := sticker.FrameAt(ctx, 0)
			vp9.SetSlowFault(nil)
			if c.failed {
				if !errors.Is(err, sandbox.ErrSlow) || slow != 2 {
					t.Fatalf("%d decodes, and %v; want 2, and the error of a slow one", slow, err)
				}
				return
			}
			if err != nil || frame == nil {
				t.Fatalf("a decode slow once left %v", err)
			}
		})
	}
}
