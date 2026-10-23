// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"testing"
)

// A picture in a color keeps its shape, its alpha, in the color, and is
// made once while it is drawn, then forgotten.
func TestImageOpsColored(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 10, B: 10, A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{R: 10, G: 200, B: 10, A: 128})
	var ops imageOps
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	ops.BeginFrame()
	im := ops.Colored(src, white)
	if got := im.At(0, 0).(color.NRGBA); got != white {
		t.Fatalf("opaque pixel %v", got)
	}
	if got := im.At(1, 0).(color.NRGBA); got != (color.NRGBA{R: 255, G: 255, B: 255, A: 128}) {
		t.Fatalf("translucent pixel %v", got)
	}
	if ops.Colored(src, white) != im {
		t.Fatal("made again in one frame")
	}
	ops.EndFrame()
	for range 3 {
		ops.BeginFrame()
		ops.EndFrame()
	}
	if len(ops.colored) != 0 {
		t.Fatalf("kept %d", len(ops.colored))
	}
}
