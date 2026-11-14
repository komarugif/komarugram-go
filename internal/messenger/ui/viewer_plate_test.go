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

// TestViewerPlateBlursWhatIsUnderIt checks the plates of the viewer, of
// the caption and of a video's controls: with the toasts' blur on, the
// plate shows what is under it in the viewer, blurred and where it is, a
// picture white on the left and black on the right going from light to
// dark across the plate over its edge; without it the plate is its fill
// alone.
func TestViewerPlateBlursWhatIsUnderIt(t *testing.T) {
	origin, size := image.Pt(50, 25), image.Pt(100, 50)
	row := func(blurred bool) (left, middle, right uint8) {
		path := filepath.Join(t.TempDir(), "plate.png")
		renderFrames(t, image.Pt(200, 100), path, func(gtx layout.Context) {
			v := &mediaViewer{backdropColor: viewerBackdrop}
			// The picture is only recorded: the plate alone can show it.
			macro := op.Record(gtx.Ops)
			paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, clip.Rect{Max: image.Pt(100, 100)}.Op())
			paint.FillShape(gtx.Ops, color.NRGBA{A: 255}, clip.Rect{Min: image.Pt(100, 0), Max: image.Pt(200, 100)}.Op())
			picture := macro.Stop()
			if blurred {
				v.behind = &picture
			}
			defer op.Offset(origin).Push(gtx.Ops).Pop()
			defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
			v.plateFill(gtx, size, origin, color.NRGBA{A: 150})
		})
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img, err := png.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		at := func(x int) uint8 { return color.NRGBAModel.Convert(img.At(x, 50)).(color.NRGBA).R }
		return at(56), at(100), at(144)
	}
	left, middle, right := row(true)
	if left < 60 || left <= middle+10 || middle <= right+10 {
		t.Errorf("a plate that blurs is %d, %d and %d from left to right, want the picture's white going to black over its edge in the middle", left, middle, right)
	}
	left, middle, right = row(false)
	if left != middle || middle != right {
		t.Errorf("a plate that does not blur is %d, %d and %d from left to right, want its fill alone", left, middle, right)
	}
}
