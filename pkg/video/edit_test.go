// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"komarugram/pkg/program"
)

// A GIF edited is encoded as one made anew: GIFPlan's preset, quality and cap, its
// scaler and box, without sound, whatever the video had. Others keep to their own.
func TestEditPlanOfAGIF(t *testing.T) {
	media := Media{Info: Info{Width: 1920, Height: 1080}, Duration: 20 * time.Second, Sound: true, AudioCodec: "aac"}
	want := PlanGIF(1920, 1080, 20*time.Second)
	gif := strings.Join(EditPlan{Media: media, GIF: true}.Args("in", "out"), " ")
	for _, s := range []string{"-preset slow", "-crf 28", "-maxrate ", "w=1280:h=720:", "lanczos", "-an", "-movflags +faststart"} {
		if !strings.Contains(gif, s) {
			t.Errorf("a GIF lacks %q: %s", s, gif)
		}
	}
	if strings.Contains(gif, "0:a:0") {
		t.Errorf("a GIF with sound: %s", gif)
	}
	if want.MaxRate <= 0 {
		t.Fatal("no cap from the length")
	}
	plain := strings.Join(EditPlan{Media: media}.Args("in", "out"), " ")
	for _, s := range []string{"-preset medium", "-crf 20", "0:a:0"} {
		if !strings.Contains(plain, s) {
			t.Errorf("a video lacks %q: %s", s, plain)
		}
	}
	for _, s := range []string{"-maxrate", "lanczos"} {
		if strings.Contains(plain, s) {
			t.Errorf("a video has %q: %s", s, plain)
		}
	}
	// The box is of what comes out: a part cut and turned upright.
	turned := EditPlan{Media: media, GIF: true, Crop: image.Rect(0, 0, 1920, 400), Angle: 90}
	if p := turned.pass(); p.Height != 1280 || p.Width >= 400 {
		t.Errorf("turned: %+v", p)
	}
}

// One too large is encoded again under a lower cap, up to what ends it.
func TestEncodeEditGIFKeepsWithinLimit(t *testing.T) {
	ffmpeg := ResolveFFmpeg("")
	if ffmpeg == "" || ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	requireEncoder(t, ffmpeg, "libx264")
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if out, err := program.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=320x240:d=1:r=25", "-c:v", "libx264", "-crf", "5", "-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	media, err := ProbeMedia(context.Background(), clip, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	plan := EditPlan{Media: media, GIF: true}
	out := filepath.Join(dir, "out.mp4")
	memory := DecodeMemory(image.Pt(media.Width, media.Height)) + gifEncodeMemory
	passes := 0
	report := func(p GIFProgress) { passes = max(passes, p.Pass) }
	first := func() int64 {
		if err := encodeEditGIF(context.Background(), ffmpeg, clip, out, plan, memory, 1<<26, report); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}()
	if passes != 1 {
		t.Errorf("%d passes with room", passes)
	}
	passes = 0
	if err := encodeEditGIF(context.Background(), ffmpeg, clip, out, plan, memory, 100, report); !errors.Is(err, ErrGIFTooLarge) {
		t.Fatalf("under an impossible limit: %v", err)
	}
	if passes != gifPasses {
		t.Errorf("%d passes, want %d", passes, gifPasses)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("what is too large stayed")
	}
	passes = 0
	limit := first * 8 / 10
	if err := encodeEditGIF(context.Background(), ffmpeg, clip, out, plan, memory, limit, report); err != nil {
		t.Fatalf("a little over the limit: %v", err)
	}
	if passes < 2 {
		t.Errorf("%d passes", passes)
	}
	if info, err := os.Stat(out); err != nil || info.Size() > limit {
		t.Errorf("came out %v, %v", info, err)
	}
}
