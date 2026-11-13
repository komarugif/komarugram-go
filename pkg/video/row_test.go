// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// solidGIF is a GIF of frames frames of 60 ms in c, size big.
func solidGIF(t *testing.T, c color.Color, size image.Point, frames int) []byte {
	t.Helper()
	g := &gif.GIF{}
	for range frames {
		im := image.NewPaletted(image.Rectangle{Max: size}, color.Palette{c})
		g.Image = append(g.Image, im)
		g.Delay = append(g.Delay, 6)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A row of videos plays in one FFmpeg as one picture, each video covering
// its tile at the sum of the widths before it, each looping at its own
// length; one is read from memory, handed to FFmpeg open.
func TestRowPlayer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("counts processes in /proc, hands a memfd")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip(err)
	}
	red, blue := color.RGBA{R: 0xff, A: 0xff}, color.RGBA{B: 0xff, A: 0xff}
	file, err := MemoryFile("red", solidGIF(t, red, image.Pt(80, 40), 5))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	path := filepath.Join(t.TempDir(), "blue.gif")
	if err := os.WriteFile(path, solidGIF(t, blue, image.Pt(30, 60), 3), 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewRowPlayer([]RowInput{
		{File: file, Width: 40, Source: image.Pt(80, 40)},
		{Path: path, Width: 24, Source: image.Pt(30, 60)},
	}, 30, 30, "")
	p.Start()
	defer p.Stop()
	seen := map[int]bool{}
	frames := map[*image.RGBA]bool{}
	var last *image.RGBA
	for until := time.Now().Add(1500 * time.Millisecond); time.Now().Before(until); {
		im, err := p.Frame()
		if err != nil {
			t.Fatal(err)
		}
		if im != nil {
			frames[im] = true
			last = im
		}
		for _, pid := range ffmpegPIDs() {
			seen[pid] = true
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(frames) < 10 {
		t.Fatalf("only %d frames of the row in several passes", len(frames))
	}
	if len(seen) != 1 {
		t.Fatalf("%d FFmpeg processes for one row", len(seen))
	}
	if got := last.Bounds().Size(); got != image.Pt(64, 30) {
		t.Fatalf("the row's picture is %v, want 64x30", got)
	}
	near := func(a, b color.RGBA) bool {
		d := func(x, y uint8) int { return max(int(x)-int(y), int(y)-int(x)) }
		return d(a.R, b.R) < 24 && d(a.G, b.G) < 24 && d(a.B, b.B) < 24
	}
	for _, c := range []struct {
		x    int
		want color.RGBA
	}{{2, red}, {37, red}, {42, blue}, {61, blue}} {
		if got := last.RGBAAt(c.x, 15); !near(got, c.want) {
			t.Errorf("at x=%d %v, want %v", c.x, got, c.want)
		}
	}
}
