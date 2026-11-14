// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import "image"

// sizeYCbCr makes dst a 4:2:0 picture of w by h, in the planes it has if
// they are large enough.
func sizeYCbCr(dst *image.YCbCr, w, h int) {
	cw, ch := (w+1)/2, (h+1)/2
	grow := func(plane []uint8, n int) []uint8 {
		if cap(plane) < n {
			return make([]uint8, n)
		}
		return plane[:n]
	}
	dst.Y, dst.Cb, dst.Cr = grow(dst.Y, w*h), grow(dst.Cb, cw*ch), grow(dst.Cr, cw*ch)
	dst.YStride, dst.CStride = w, cw
	dst.SubsampleRatio = image.YCbCrSubsampleRatio420
	dst.Rect = image.Rect(0, 0, w, h)
}
