// SPDX-License-Identifier: Unlicense OR MIT

package player

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/pkg/miniapp"
)

// TestChromiumFit checks that the window takes the shape of the video once
// the video tells its size.
func TestChromiumFit(t *testing.T) {
	if !miniapp.Available() {
		t.Skip("no Chromium-based browser")
	}
	path := os.Getenv("PLAYER_VIDEO")
	if path == "" {
		paths, _ := filepath.Glob("../../assets/rigby/*.webm")
		if len(paths) == 0 {
			t.Skip("no PLAYER_VIDEO or ../../assets/rigby/*.webm to play")
		}
		path = paths[0]
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := Serve("video", file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	p, err := openChromium(context.Background(), stream.URL(), []string{"--headless=new", "--mute-audio"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var size struct{ VW, VH, W, H float64 }
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		answer, err := p.page.Eval(context.Background(), `(function () {
		  var v = document.getElementById('player');
		  return JSON.stringify({vw: v.videoWidth, vh: v.videoHeight, w: innerWidth, h: innerHeight});
		})()`)
		if err == nil && json.Unmarshal([]byte(answer), &size) == nil && size.VW > 0 && size.W > 0 &&
			math.Abs(size.W/size.H-size.VW/size.VH) < 0.02 {
			t.Logf("video %vx%v, window %vx%v", size.VW, size.VH, size.W, size.H)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the window never took the shape of the video: %+v", size)
}

// TestChromiumSizeArgs checks the size the browser's window opens at.
func TestChromiumSizeArgs(t *testing.T) {
	for _, c := range []struct {
		kind          Kind
		width, height int
		want          string
	}{
		{Chromium, 640, 360, "--window-size=640,360"},
		{Chromium, 1920, 1080, "--window-size=1280,720"},
		{Chromium, 720, 1280, "--window-size=450,800"},
		{Chromium, 240, 240, "--window-size=400,400"},
		{Chromium, 200, 1000, "--window-size=160,800"},
		{Chromium, 0, 0, ""},
		{VLC, 640, 360, ""},
	} {
		got := ""
		if args := c.kind.SizeArgs(c.width, c.height); len(args) > 0 {
			got = args[0]
		}
		if got != c.want {
			t.Errorf("%s %dx%d: got %q, want %q", c.kind, c.width, c.height, got, c.want)
		}
	}
}

// TestChromiumOpensAtSize checks that the window opens at the size
// SizeArgs gave, which is all it gets where it may not resize itself, as on
// Wayland: here the video never tells its size.
func TestChromiumOpensAtSize(t *testing.T) {
	if !miniapp.Available() {
		t.Skip("no Chromium-based browser")
	}
	stream, err := Serve("video", bytes.NewReader(make([]byte, 64<<10)), 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	extra := append([]string{"--headless=new", "--mute-audio"}, Chromium.SizeArgs(720, 1280)...)
	p, err := openChromium(context.Background(), stream.URL(), extra)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	answer, err := p.page.Eval(context.Background(), `innerWidth + "x" + innerHeight`)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "450x800" {
		t.Errorf("the window opened at %s, want 450x800", answer)
	}
}
