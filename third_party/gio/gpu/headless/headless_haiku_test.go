// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package headless

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/op"
	"gioui.org/op/paint"
)

// Haiku must render without a BApplication or a window. Alternate two
// contexts and recreate them to cover switching and releasing current state.
func TestHaikuHeadlessContexts(t *testing.T) {
	for range 3 {
		func() {
			a, err := NewWindow(32, 24)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Release()
			b, err := NewWindow(16, 12)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Release()
			for _, w := range []*Window{a, b, a, b} {
				var ops op.Ops
				want := color.NRGBA{R: 73, G: 151, B: 219, A: 255}
				paint.Fill(&ops, want)
				if err := w.Frame(&ops); err != nil {
					t.Fatal(err)
				}
				img := image.NewRGBA(image.Rectangle{Max: w.Size()})
				if err := w.Screenshot(img); err != nil {
					t.Fatal(err)
				}
				for y := range img.Rect.Dy() {
					for x := range img.Rect.Dx() {
						if got := img.RGBAAt(x, y); got != (color.RGBA{R: want.R, G: want.G, B: want.B, A: want.A}) {
							t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
						}
					}
				}
			}
		}()
	}
}
