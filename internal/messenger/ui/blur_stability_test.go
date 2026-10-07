// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// blurredMenu draws the pattern of blurredUnder under a menu of 200×80 at
// (20, 20), of which shown is seen, as contextMenu draws a menu opening from
// a corner, and returns the frame.
func blurredMenu(t *testing.T, shown image.Rectangle) image.Image {
	t.Helper()
	path := filepath.Join(t.TempDir(), "menu.png")
	renderFrames(t, image.Pt(240, 120), path, func(gtx layout.Context) {
		page := markPattern(gtx)
		defer op.Offset(image.Pt(20, 20)).Push(gtx.Ops).Pop()
		defer clip.UniformRRect(shown, 8).Push(gtx.Ops).Pop()
		layoutBackdrop(gtx, image.Pt(200, 80), image.Pt(20, 20), page)
	})
	return decodePNG(t, path)
}

// The blurred backdrop of a menu opening from a bottom or a right corner
// keeps its place, though the top or the left edge of what is seen of the
// menu moves with every frame.
func TestBlurDoesNotSwimWithTheCornerAMenuOpensFrom(t *testing.T) {
	for _, c := range []struct {
		name  string
		shown func(step int) image.Rectangle
		// inner is where the frames are compared, away from the edges
		// that move.
		inner image.Rectangle
	}{
		{"from the bottom", func(step int) image.Rectangle { return image.Rect(0, 30+step, 200, 80) }, image.Rect(30, 80, 190, 90)},
		{"from the right", func(step int) image.Rectangle { return image.Rect(100+step, 0, 200, 80) }, image.Rect(150, 30, 210, 90)},
	} {
		before := blurredMenu(t, c.shown(0))
		for step := 1; step <= 4; step++ {
			after := blurredMenu(t, c.shown(step))
			worst := 0
			for y := c.inner.Min.Y; y < c.inner.Max.Y; y++ {
				for x := c.inner.Min.X; x < c.inner.Max.X; x++ {
					a, _, _, _ := before.At(x, y).RGBA()
					b, _, _, _ := after.At(x, y).RGBA()
					d := int(a>>8) - int(b>>8)
					worst = max(worst, d, -d)
				}
			}
			if worst > 1 {
				t.Errorf("%s: moved by %d px, the blurred backdrop differs by %d/255", c.name, step, worst)
			}
		}
	}
}

// markPattern records a pattern of small marks over 240×120, sharp enough
// that the blur of it shows any shift.
func markPattern(gtx layout.Context) op.CallOp {
	macro := op.Record(gtx.Ops)
	for x := 0; x < 240; x += 6 {
		for y := 0; y < 120; y += 6 {
			if (x/6+y/6)%3 == 0 {
				paint.FillShape(gtx.Ops, color.NRGBA{A: 255}, clip.Rect{Min: image.Pt(x, y), Max: image.Pt(x+3, y+4)}.Op())
			}
		}
	}
	return macro.Stop()
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// blurredUnder draws a pattern of small marks, blurred under a clip of width
// w at a fixed place, as a menu opening does, and returns the frame.
func blurredUnder(t *testing.T, w int) image.Image {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blur.png")
	renderFrames(t, image.Pt(240, 120), path, func(gtx layout.Context) {
		page := markPattern(gtx)
		defer clip.UniformRRect(image.Rect(20, 20, 20+w, 100), 8).Push(gtx.Ops).Pop()
		layoutBackdrop(gtx, image.Pt(w, 80), image.Pt(20, 20), page)
	})
	return decodePNG(t, path)
}

// The blurred backdrop of a menu that is opening keeps its place: the size
// of the layer changes with every frame, and the blurred text behind the
// menu must not swim with it.
func TestBlurDoesNotSwimWithTheSizeOfItsClip(t *testing.T) {
	for _, w := range []int{66, 68, 70, 100, 101, 102, 103} {
		before, after := blurredUnder(t, w-2), blurredUnder(t, w)
		worst := 0
		// Away from the edge that moves, where the clip cuts the pattern.
		for y := 30; y < 90; y++ {
			for x := 30; x < 20+w-30-2; x++ {
				a, _, _, _ := before.At(x, y).RGBA()
				b, _, _, _ := after.At(x, y).RGBA()
				d := int(a>>8) - int(b>>8)
				worst = max(worst, d, -d)
			}
		}
		if worst > 1 {
			t.Errorf("width %d: the blurred backdrop differs by %d/255 from that at %d", w, worst, w-2)
		}
	}
}

// Unlike the older corner tests, the entire blur capture moves here: a
// bottom-anchored menu changes its top and height when reactions expand.
// Compare fixed screen pixels, well away from either capture's boundaries.
func TestBlurDoesNotSwimWhenMenuCaptureMoves(t *testing.T) {
	render := func(rect image.Rectangle) image.Image {
		path := filepath.Join(t.TempDir(), "moving-menu.png")
		renderFrames(t, image.Pt(300, 300), path, func(gtx layout.Context) {
			macro := op.Record(gtx.Ops)
			for y := 0; y < 300; y += 6 {
				for x := 0; x < 300; x += 6 {
					if (x/6+y/6)%3 == 0 {
						paint.FillShape(gtx.Ops, color.NRGBA{A: 255}, clip.Rect(image.Rect(x, y, x+3, y+4)).Op())
					}
				}
			}
			page := macro.Stop()
			defer op.Offset(rect.Min).Push(gtx.Ops).Pop()
			defer clip.UniformRRect(image.Rectangle{Max: rect.Size()}, 8).Push(gtx.Ops).Pop()
			layoutBackdrop(gtx, rect.Size(), rect.Min, page)
		})
		return decodePNG(t, path)
	}
	rect := image.Rect(96, 96, 260, 260)
	before := render(rect)
	for _, shift := range []image.Point{{0, 1}, {0, 3}, {0, 17}, {1, 0}, {17, 0}} {
		afterRect := rect
		afterRect.Min = afterRect.Min.Sub(shift)
		after := render(afterRect)
		worst := 0
		for y := 160; y < 210; y++ {
			for x := 160; x < 210; x++ {
				a, _, _, _ := before.At(x, y).RGBA()
				b, _, _, _ := after.At(x, y).RGBA()
				d := int(a>>8) - int(b>>8)
				worst = max(worst, d, -d)
			}
		}
		if worst > 1 {
			t.Errorf("capture moved by %v: static backdrop differs by %d/255", shift, worst)
		}
	}
}
