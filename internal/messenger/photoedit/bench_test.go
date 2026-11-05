// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import (
	"image"
	"testing"
)

// BenchmarkPreviewStroke is a frame of a stroke being drawn, of 500 points, over
// 100 strokes done, in a view of 1600×1000.
func BenchmarkPreviewStroke(b *testing.B) {
	size := image.Pt(1600, 1000)
	base := grayPicture(size)
	var p Preview
	p.SetBase(base, wholeCanvas(size), size)
	var list []*Element
	for i := range 100 {
		list = append(list, stroke(Pen, 6, Point{0, float64(i * 10)}, Point{1600, float64(i * 10)}))
	}
	p.Render(list, true, nil)
	points := make([]Point, 500)
	for i := range points {
		points[i] = Point{float64(i) * 3, 500 + float64(i%40)}
	}
	for i := 0; b.Loop(); i++ {
		live := stroke(Pen, 6, points[:200+i%300]...)
		p.Render(append(list[:100:100], live), false, nil)
	}
}
