// SPDX-License-Identifier: Unlicense OR MIT

package rectanim

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// TestRenderScenes draws frames of every scene in RECTANIM_SCENES, a
// directory of scene files, into RECTANIM_PNG_DIR: one sheet per scene,
// RECTANIM_FRAMES frames (12 by default) across its length, eight to a
// row, for looking at them.
func TestRenderScenes(t *testing.T) {
	dir, out := os.Getenv("RECTANIM_SCENES"), os.Getenv("RECTANIM_PNG_DIR")
	if dir == "" || out == "" {
		t.Skip("set RECTANIM_SCENES and RECTANIM_PNG_DIR")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(f, "index.json") {
			continue
		}
		s, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		frames, scale := 12, float32(2)
		if n, err := strconv.Atoi(os.Getenv("RECTANIM_FRAMES")); err == nil && n > 0 {
			frames = n
		}
		cw := int(s.ViewBox[2]*s.Stage*scale) + 40
		ch := int(s.ViewBox[3]*scale*1.6) + 40
		cols := min(frames, 8)
		rows := (frames + cols - 1) / cols
		size := image.Pt(cw*cols, ch*rows)
		win, err := headless.NewWindow(size.X, size.Y)
		if err != nil {
			t.Fatal(err)
		}
		ops := new(op.Ops)
		paint.FillShape(ops, color.NRGBA{R: 250, G: 248, B: 245, A: 255}, clip.Rect{Max: size}.Op())
		for i := 0; i < frames; i++ {
			at := s.Duration * float64(i) / float64(frames)
			// The picture's box sits low in its cell, to leave room above for jumps.
			view := f32.Affine2D{}.Offset(f32.Pt(-s.ViewBox[0], -s.ViewBox[1])).Scale(f32.Point{}, f32.Pt(scale, scale)).
				Offset(f32.Pt(float32(i%cols*cw)+20, float32(i/cols*ch)+float32(ch)-s.ViewBox[3]*scale-20))
			Paint(ops, s.Frame(at), view)
		}
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: size})
		if err := win.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		win.Release()
		w, err := os.Create(filepath.Join(out, s.Name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(w, img)
		w.Close()
	}
}
