// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"context"
	"image"
	"testing"
)

// BenchmarkRender takes pictures from a helper on a device that decodes
// nothing, scaled to a tile, as the list of saved GIFs does: what the
// window's process spends on them, and allocates.
func BenchmarkRender(b *testing.B) {
	file := testFile(b)
	self := testClient(b, "fake")
	v, err := self.Open(file)
	if err != nil {
		b.Fatal(err)
	}
	defer v.Close(context.Background())
	dst := image.NewRGBA(image.Rect(0, 0, 160, 284))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := v.Next(context.Background()); err != nil {
			b.Fatal(err)
		}
		if v.PictureInto(dst) == nil {
			b.Fatal(v.Err())
		}
	}
}
