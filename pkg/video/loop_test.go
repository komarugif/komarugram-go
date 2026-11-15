// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"image"
	"image/color"
	"image/gif"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"komarugram/internal/testenv"
)

// ffmpegPIDs lists the ffmpeg processes this test started.
func ffmpegPIDs() []int {
	paths, _ := filepath.Glob("/proc/[0-9]*/stat")
	self := strconv.Itoa(os.Getpid())
	var pids []int
	for _, path := range paths {
		stat, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s := string(stat)
		open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || end < open {
			continue
		}
		fields := strings.Fields(s[end+1:])
		if s[open+1:end] == "ffmpeg" && len(fields) > 1 && fields[1] == self {
			pid, _ := strconv.Atoi(strings.Fields(s)[0])
			pids = append(pids, pid)
		}
	}
	return pids
}

// TestPlayerLoopsInOneProcess checks that a looping clip keeps its ffmpeg
// instead of starting one for every pass.
func TestPlayerLoopsInOneProcess(t *testing.T) {
	// It counts every ffmpeg of the system: another run's are counted too.
	testenv.Integration(t, "runs FFmpeg")
	if runtime.GOOS != "linux" {
		t.Skip("counts processes in /proc")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip(err)
	}
	// Five frames of 60 ms: a pass takes 0.3 s.
	g := &gif.GIF{}
	palette := color.Palette{color.Black, color.White}
	for n := range 5 {
		im := image.NewPaletted(image.Rect(0, 0, 32, 32), palette)
		im.SetColorIndex(n*6, 16, 1)
		g.Image = append(g.Image, im)
		g.Delay = append(g.Delay, 6)
	}
	path := filepath.Join(t.TempDir(), "loop.gif")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
	f.Close()
	p, err := NewPlayer(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()
	seen := map[int]bool{}
	frames := map[*image.RGBA]bool{}
	for until := time.Now().Add(1500 * time.Millisecond); time.Now().Before(until); {
		im, err := p.Frame()
		if err != nil {
			t.Fatal(err)
		}
		if im != nil {
			frames[im] = true
		}
		for _, pid := range ffmpegPIDs() {
			seen[pid] = true
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(frames) < 10 {
		t.Fatalf("only %d frames in several passes", len(frames))
	}
	if len(seen) != 1 {
		t.Fatalf("%d ffmpeg processes for one looping clip", len(seen))
	}
}
