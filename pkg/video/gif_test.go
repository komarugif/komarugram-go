// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// A GIF fits in the box of HD turned as the video is, keeps its proportions
// on even sides, and a smaller video keeps its size.
func TestGIFSize(t *testing.T) {
	for _, c := range []struct{ w, h, wantW, wantH int }{
		{1920, 1080, 1280, 720},
		{1080, 1920, 720, 1280},
		{3840, 1600, 1280, 534},
		{1080, 1080, 720, 720},
		{640, 360, 640, 360},
		{721, 405, 722, 406},
		{0, 100, 0, 0},
	} {
		if w, h := GIFSize(c.w, c.h); w != c.wantW || h != c.wantH {
			t.Errorf("GIFSize(%d, %d) = %d×%d, want %d×%d", c.w, c.h, w, h, c.wantW, c.wantH)
		}
	}
}

// The encoding is the bot's, without sound, scaled, with a cap that keeps
// the length within the size; one too large is encoded under a lower cap.
func TestPlanGIF(t *testing.T) {
	p := PlanGIF(1920, 1080, 20*time.Second)
	if p.CRF != 28 || p.Preset != "slow" || p.Width != 1280 || p.Height != 720 {
		t.Fatalf("plan %+v", p)
	}
	if bytes := float64(p.MaxRate) / 8 * 20; bytes > GIFMaxBytes || bytes < GIFMaxBytes*0.8 {
		t.Fatalf("cap %d b/s makes %.0f bytes in 20 s", p.MaxRate, bytes)
	}
	args := strings.Join(p.Args("in.mp4", "out.mp4"), " ")
	for _, want := range []string{"-i in.mp4", "-map 0:v:0 -an", "scale=1280:720", "-c:v libx264", "-preset slow", "-crf 28", "-maxrate ", "-progress pipe:1", "-f mp4"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	if !strings.HasSuffix(args, " out.mp4") {
		t.Errorf("args end %s", args)
	}
	again := p.Again(GIFMaxBytes*2, 20*time.Second)
	if again.MaxRate >= p.MaxRate/2+1 || again.CRF <= p.CRF {
		t.Fatalf("again %+v after %+v", again, p)
	}
	if long := PlanGIF(640, 360, 0); long.MaxRate != 0 || slices.Contains(long.Args("a", "b"), "-maxrate") {
		t.Fatalf("a video of unknown length is capped: %+v", long)
	}
}

// FFmpeg's -progress is read a block at a time, each ended by progress=.
func TestReadGIFProgress(t *testing.T) {
	out := `frame=12
fps=24.00
bitrate=N/A
total_size=48
out_time_us=500000
out_time_ms=500000
out_time=00:00:00.500000
speed=1.2x
progress=continue
frame=48
total_size=196608
out_time_us=N/A
progress=continue
frame=96
total_size=393216
out_time_us=2000000
progress=end
`
	var got []GIFProgress
	err := ReadGIFProgress(strings.NewReader(out), GIFProgress{Pass: 2, Total: 2 * time.Second}, func(p GIFProgress) { got = append(got, p) })
	if err != nil || len(got) != 3 {
		t.Fatalf("%d reports, %v", len(got), err)
	}
	if got[0].Frame != 12 || got[0].Done != 500*time.Millisecond || got[0].Pass != 2 || got[0].Share() != .25 {
		t.Errorf("first %+v share %v", got[0], got[0].Share())
	}
	// N/A leaves the time as it was.
	if got[1].Frame != 48 || got[1].Size != 196608 || got[1].Done != 500*time.Millisecond {
		t.Errorf("second %+v", got[1])
	}
	if !got[2].End || got[2].Share() != 1 {
		t.Errorf("last %+v", got[2])
	}
	if (GIFProgress{Done: time.Second}).Share() >= 0 {
		t.Error("a share of an unknown length")
	}
}
