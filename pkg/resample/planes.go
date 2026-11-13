// SPDX-License-Identifier: Unlicense OR MIT

package resample

import "image"

// ResizePlanes is Resize kept the way video keeps a picture: luma and alpha
// at w×h, chroma at half of that each way, 2.5 bytes a pixel in place of
// the 4 of RGBA, for a loop of frames held in memory. The colours are not
// premultiplied, and are the frame's own: ConvertAlpha, with the scaler's
// Colors, makes the picture Resize would, but for chroma as coarse as a
// video's at this size.
func (s *VideoScaler) ResizePlanes(im, alpha *image.YCbCr, w, h int) *image.NYCbCrA {
	out := image.NewNYCbCrA(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	sw, sh := im.Rect.Dx(), im.Rect.Dy()
	if w <= 0 || h <= 0 || sw <= 0 || sh <= 0 {
		return out
	}
	row := s.premultiplied(im, alpha, blocks(sw, w), blocks(sh, h), w, h)
	// The chroma of a cell of 2×2 pixels is theirs weighed by their alpha:
	// sums holds, for each cell, both chroma premultiplied and the alpha.
	cw := (w + 1) / 2
	sums := make([]float32, cw*3)
	for y := range h {
		acc := row(y)[:w*4]
		luma := out.Y[y*out.YStride : y*out.YStride+w]
		mask := out.A[y*out.AStride : y*out.AStride+w]
		for x := range w {
			q := acc[x*4 : x*4+4 : x*4+4]
			a := clamp(q[3], 255)
			mask[x] = uint8(a + .5)
			if a > 0 {
				luma[x] = uint8(clamp(q[0]*255/a, 255) + .5)
			}
			c := sums[x/2*3 : x/2*3+3 : x/2*3+3]
			c[0] += q[1]
			c[1] += q[2]
			c[2] += a
		}
		if y%2 == 0 && y != h-1 {
			continue
		}
		cb := out.Cb[y/2*out.CStride : y/2*out.CStride+cw]
		cr := out.Cr[y/2*out.CStride : y/2*out.CStride+cw]
		for x := range cw {
			c := sums[x*3 : x*3+3 : x*3+3]
			u, v := float32(128), float32(128)
			if c[2] > 0 {
				u += c[0] * 255 / c[2]
				v += c[1] * 255 / c[2]
			}
			cb[x], cr[x] = uint8(clamp(u, 255)+.5), uint8(clamp(v, 255)+.5)
		}
		clear(sums)
	}
	return out
}

// ConvertAlpha puts im in dst, of its size, as colors say, premultiplied by
// its alpha, as image.RGBA and Gio expect: each pixel with the chroma it
// lies on.
func ConvertAlpha(dst *image.RGBA, im *image.NYCbCrA, colors Colors) {
	c := colors.conversion()
	const one = 1 << 16
	fixed := func(v float32) int32 { return int32(v*one + .5) }
	rv, gu, gv, bu := fixed(c.rv), fixed(c.gu), fixed(c.gv), fixed(c.bu)
	// What each level of luma is worth, once.
	var levels [256]int32
	for i := range levels {
		levels[i] = int32((float32(i) - c.black) * c.gain * one)
	}
	cx, cy := chromaShift(im.SubsampleRatio)
	w, h := min(dst.Rect.Dx(), im.Rect.Dx()), min(dst.Rect.Dy(), im.Rect.Dy())
	for y := range h {
		luma := im.Y[y*im.YStride : y*im.YStride+w]
		mask := im.A[y*im.AStride : y*im.AStride+w]
		row := (y >> cy) * im.CStride
		cb, cr := im.Cb[row:], im.Cr[row:]
		pix := dst.Pix[y*dst.Stride : y*dst.Stride+w*4]
		for x, l := range luma {
			p := pix[x*4 : x*4+4 : x*4+4]
			a := uint32(mask[x])
			if a == 0 {
				p[0], p[1], p[2], p[3] = 0, 0, 0, 0
				continue
			}
			u, v := int32(cb[x>>cx])-128, int32(cr[x>>cx])-128
			base := levels[l] + one/2
			r, g, b := level(base+rv*v), level(base-gu*u-gv*v), level(base+bu*u)
			if a != 255 {
				// x×a/255, rounded: exact for bytes.
				r = uint8((uint32(r)*a*257 + 0x8080) >> 16)
				g = uint8((uint32(g)*a*257 + 0x8080) >> 16)
				b = uint8((uint32(b)*a*257 + 0x8080) >> 16)
			}
			p[0], p[1], p[2], p[3] = r, g, b, uint8(a)
		}
	}
}
