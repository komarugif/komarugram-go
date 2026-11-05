// SPDX-License-Identifier: Unlicense OR MIT

package resample

import "image"

// Matrix is how a picture's luma and chroma were made of its red, green and blue.
type Matrix uint8

const (
	// BT601 is what JPEG uses, and video of standard definition.
	BT601 Matrix = iota
	// BT709 is what video of high definition uses.
	BT709
	// BT2020 is what video of ultra high definition uses.
	BT2020
)

// Colors says how the planes of a picture become RGB. The zero value is JPEG's, as
// color.YCbCrToRGB has it, the full range in BT.601. Video mostly has its levels in
// the range from 16 to 235, read as black and white: read as JPEG's, black is gray and
// white is dim, and the colors are as much less saturated.
type Colors struct {
	Matrix Matrix
	// Limited is set for levels in the limited range.
	Limited bool
}

// GuessMatrix is the matrix of a video that does not tell it, as players guess it, by
// its size.
func GuessMatrix(width, height int) Matrix {
	if width >= 1280 || height >= 720 {
		return BT709
	}
	return BT601
}

// conversion is Colors as factors: RGB is gain×(Y-black) plus the chroma, from 128,
// times the rest.
type conversion struct {
	gain, black    float32
	rv, gu, gv, bu float32
}

func (c Colors) conversion() conversion {
	kr, kb := float32(0.299), float32(0.114)
	switch c.Matrix {
	case BT709:
		kr, kb = 0.2126, 0.0722
	case BT2020:
		kr, kb = 0.2627, 0.0593
	}
	kg := 1 - kr - kb
	out := conversion{gain: 1}
	chroma := float32(1)
	if c.Limited {
		out.gain, out.black = 255.0/219.0, 16
		chroma = 255.0 / 224.0
	}
	out.rv = (2 - 2*kr) * chroma
	out.bu = (2 - 2*kb) * chroma
	out.gu = (2 - 2*kb) * kb / kg * chroma
	out.gv = (2 - 2*kr) * kr / kg * chroma
	return out
}

// Convert puts im in dst, of its size, as colors say, opaque: each pixel with the
// chroma it lies on, as image/draw does for JPEG's.
func Convert(dst *image.RGBA, im *image.YCbCr, colors Colors) {
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
		row := (y >> cy) * im.CStride
		cb, cr := im.Cb[row:], im.Cr[row:]
		pix := dst.Pix[y*dst.Stride : y*dst.Stride+w*4]
		for x, l := range luma {
			u, v := int32(cb[x>>cx])-128, int32(cr[x>>cx])-128
			base := levels[l] + one/2
			p := pix[x*4 : x*4+4 : x*4+4]
			p[0] = level(base + rv*v)
			p[1] = level(base - gu*u - gv*v)
			p[2] = level(base + bu*u)
			p[3] = 255
		}
	}
}

// level is a value in 16.16, within what a byte holds.
func level(v int32) uint8 {
	switch {
	case v < 0:
		return 0
	case v >= 255<<16:
		return 255
	}
	return uint8(v >> 16)
}
