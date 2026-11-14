// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"encoding/binary"
	"image"
)

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

// splitUV parts the interleaved chroma of NV12, uv, into cb and cr, as
// many samples as cb holds: eight bytes at a time.
func splitUV(cb, cr, uv []byte) {
	n := len(cb)
	cr, uv = cr[:n], uv[:2*n]
	x := 0
	for ; x+4 <= n; x += 4 {
		v := binary.LittleEndian.Uint64(uv[2*x:])
		// The even bytes are Cb, the odd ones Cr: each gathered into the
		// low half of a word.
		b := v & 0x00ff00ff00ff00ff
		r := v >> 8 & 0x00ff00ff00ff00ff
		b = (b | b>>8) & 0x0000ffff0000ffff
		r = (r | r>>8) & 0x0000ffff0000ffff
		binary.LittleEndian.PutUint32(cb[x:], uint32(b|b>>16))
		binary.LittleEndian.PutUint32(cr[x:], uint32(r|r>>16))
	}
	for ; x < n; x++ {
		cb[x], cr[x] = uv[2*x], uv[2*x+1]
	}
}
