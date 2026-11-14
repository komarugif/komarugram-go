// SPDX-License-Identifier: Unlicense OR MIT

package wallpaper

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func solidJPEG(t *testing.T, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 0xff
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The picture's file is written in place, with the registry's key and the
// path left as they were, as Windows 7's themes do when the wallpaper
// changes: the watcher has to notice it, and draw the new picture.
func TestWatcherSeesTheFileWrittenInPlace(t *testing.T) {
	dir, err := os.MkdirTemp("", "wallpaper")
	if err != nil {
		t.Fatal(err)
	}
	// The watcher lets go of the folder on its own goroutine, a moment after
	// Close, and a folder watched cannot be removed before that.
	t.Cleanup(func() {
		for deadline := time.Now().Add(5 * time.Second); os.RemoveAll(dir) != nil && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		}
	})
	path := filepath.Join(dir, "TranscodedWallpaper.jpg")
	if err := os.WriteFile(path, solidJPEG(t, color.NRGBA{R: 255, A: 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	was := sourceOf
	sourceOf = func() (string, Fit, bool, color.NRGBA) { return path, Fill, false, color.NRGBA{A: 255} }
	defer func() { sourceOf = was }()

	var changes atomic.Int32
	w := Watch(View{Window: 1}, func() { changes.Add(1) })
	defer w.Close()
	wait := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatalf("%s: not in 5 s, %d changes told", what, changes.Load())
	}
	wait("the first desktop", func() bool { return w.Desktop() != nil })
	first := w.Desktop()
	told := changes.Load()
	// The picture is of another color, as a theme's is: what the file is made
	// of is not what the key tells.
	if err := os.WriteFile(path, solidJPEG(t, color.NRGBA{B: 255, A: 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	wait("the change", func() bool { return changes.Load() > told && w.Desktop() != first })
}
