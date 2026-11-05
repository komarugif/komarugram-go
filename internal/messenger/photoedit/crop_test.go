// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import (
	"image"
	"math"
	"testing"
)

func TestGrabAt(t *testing.T) {
	crop := image.Rect(10, 10, 90, 90)
	if g, ok := GrabAt(crop, Point{10, 10}, 3); !ok || !g.Left || !g.Top || g.Right || g.Bottom {
		t.Errorf("corner: %+v %v", g, ok)
	}
	if g, ok := GrabAt(crop, Point{90, 50}, 3); !ok || !g.Right || g.Top || g.Bottom {
		t.Errorf("edge: %+v %v", g, ok)
	}
	if g, ok := GrabAt(crop, Point{50, 50}, 3); !ok || g != (Grab{}) {
		t.Errorf("inside: %+v %v", g, ok)
	}
	if _, ok := GrabAt(crop, Point{50, 95}, 3); ok {
		t.Error("beyond it")
	}
}

func TestDragCrop(t *testing.T) {
	size := image.Pt(100, 100)
	start := image.Rect(10, 10, 60, 60)
	if got := DragCrop(start, Grab{}, Point{100, -100}, size, 0, 10); got != image.Rect(50, 0, 100, 50) {
		t.Errorf("moved beyond the picture: %v", got)
	}
	if got := DragCrop(start, Grab{Right: true}, Point{-100, 0}, size, 0, 10); got != image.Rect(10, 10, 20, 60) {
		t.Errorf("under the smallest: %v", got)
	}
	if got := DragCrop(start, Grab{Left: true, Top: true}, Point{-100, -100}, size, 0, 10); got != image.Rect(0, 0, 60, 60) {
		t.Errorf("past the corner: %v", got)
	}
	// A square stays square and in the picture, whichever way it goes.
	for _, c := range []struct {
		g Grab
		d Point
	}{
		{Grab{Right: true, Bottom: true}, Point{100, 5}},
		{Grab{Left: true, Top: true}, Point{-100, -3}},
		{Grab{Right: true}, Point{200, 0}},
		{Grab{Bottom: true}, Point{0, 200}},
		{Grab{Left: true}, Point{-5, 0}},
	} {
		got := DragCrop(start, c.g, c.d, size, 1, 10)
		if math.Abs(float64(got.Dx()-got.Dy())) > 1 || !got.In(image.Rectangle{Max: size}) {
			t.Errorf("%+v by %v: %v", c.g, c.d, got)
		}
	}
}

func TestFitAspect(t *testing.T) {
	if got := FitAspect(image.Rect(0, 0, 100, 50), 1); got != image.Rect(25, 0, 75, 50) {
		t.Errorf("square of a wide one: %v", got)
	}
	if got := FitAspect(image.Rect(0, 0, 100, 100), 2); got != image.Rect(0, 25, 100, 75) {
		t.Errorf("wide of a square: %v", got)
	}
}
