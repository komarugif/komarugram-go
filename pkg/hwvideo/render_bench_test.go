// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"context"
	"image"
	"testing"

	"komarugram/pkg/resample"
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

// BenchmarkTileCPU is what the helper's CPU does for a picture of
// 720x1280 shown in a tile of 160x284, as the saved GIFs are: read back
// whole and scaled on the CPU (the picture split out of the readback, then
// resample.VideoScaler), or scaled on the GPU, where only the tile's
// pixels are copied out. The GPU's own time is not in it.
func BenchmarkTileCPU(b *testing.B) {
	const pitch, rows, w, h = 720, 1280, 160, 284
	nv12 := make([]byte, pitch*rows*3/2)
	for i := range nv12 {
		nv12[i] = byte(i * 7 / 5)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for _, rough := range []bool{false, true} {
		name := "cpu"
		if rough {
			name = "cpu-rough"
		}
		b.Run(name, func(b *testing.B) {
			scaler := resample.VideoScaler{Colors: resample.Colors{Matrix: resample.BT709, Limited: true}, Rough: rough}
			var picture image.YCbCr
			b.ReportAllocs()
			for b.Loop() {
				sizeYCbCr(&picture, pitch, rows)
				for y := range rows {
					copy(picture.Y[y*picture.YStride:y*picture.YStride+pitch], nv12[y*pitch:])
				}
				for y := range rows / 2 {
					splitUV(picture.Cb[y*picture.CStride:(y+1)*picture.CStride], picture.Cr[y*picture.CStride:(y+1)*picture.CStride], nv12[pitch*rows+y*pitch:])
				}
				scaler.ResizeInto(dst, &picture, nil)
			}
		})
	}
	b.Run("gpu", func(b *testing.B) {
		out := make([]byte, w*h*4)
		b.ReportAllocs()
		for b.Loop() {
			copy(dst.Pix, out)
		}
	})
}
