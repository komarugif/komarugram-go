// Package resample scales decoded images to premultiplied RGBA with a
// Catmull-Rom filter, streaming through the source one row at a time.
//
// golang.org/x/image/draw does the same filtering but keeps its horizontal
// pass for the whole source height in float64: dstW×srcH×32 bytes, 60 MB for
// one phone photo. Here only as many filtered rows as the vertical filter
// spans are kept, so a resize costs the destination image plus a few
// hundred kilobytes.
package resample

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/bits"
)

// Resize returns src scaled to w×h. Downscaling widens the filter by the
// scale factor, so every source pixel contributes and fine detail does not
// alias. The source's alpha is honoured; the result is premultiplied, as
// image.RGBA and Gio expect.
func Resize(src image.Image, w, h int) *image.RGBA {
	sb := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 || sb.Empty() {
		return dst
	}
	if im, ok := src.(*image.YCbCr); ok && sb.Min == (image.Point{}) {
		resizeYCbCr(dst, im, Colors{})
		return dst
	}
	read := rowReader(src)
	sc := newScaler(4, sb.Dx(), sb.Dy(), w, h, func(y int, line []float32) { read(sb.Min.Y+y, line) })
	for y := range h {
		acc := sc.row(y)
		pix := dst.Pix[y*dst.Stride : y*dst.Stride+w*4]
		for x := 0; x < w*4; x += 4 {
			// Catmull-Rom overshoots near edges; premultiplied colour must
			// stay within its alpha.
			a := clamp(acc[x+3], 255)
			pix[x] = uint8(clamp(acc[x], a) + .5)
			pix[x+1] = uint8(clamp(acc[x+1], a) + .5)
			pix[x+2] = uint8(clamp(acc[x+2], a) + .5)
			pix[x+3] = uint8(a + .5)
		}
	}
	return dst
}

// resizeYCbCr scales the luma and chroma planes of a decoded JPEG each at
// its own resolution and converts to RGB only the destination pixels. The
// filter is linear and the conversion affine, so this is the same picture
// as converting first, for a quarter of the work with 4:2:0 chroma. Chroma
// is also filtered rather than repeated, as image.YCbCr.At would. Colors says
// how the planes are read: the zero value is JPEG's.
func resizeYCbCr(dst *image.RGBA, im *image.YCbCr, colors Colors) {
	c := colors.conversion()
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	sw, sh := im.Rect.Dx(), im.Rect.Dy()
	cw, ch := chromaSize(im.SubsampleRatio, sw, sh)
	plane := func(pix []uint8, stride, pw, ph int) *scaler {
		return newScaler(1, pw, ph, w, h, func(y int, line []float32) {
			row := pix[y*stride : y*stride+pw]
			for i, v := range row {
				line[i] = float32(v)
			}
		})
	}
	ys := plane(im.Y, im.YStride, sw, sh)
	cbs := plane(im.Cb, im.CStride, cw, ch)
	crs := plane(im.Cr, im.CStride, cw, ch)
	for y := range h {
		yr, cb, cr := ys.row(y), cbs.row(y), crs.row(y)
		pix := dst.Pix[y*dst.Stride : y*dst.Stride+w*4]
		for x := range w {
			l, u, v := (yr[x]-c.black)*c.gain, cb[x]-128, cr[x]-128
			pix[x*4] = uint8(clamp(l+c.rv*v, 255) + .5)
			pix[x*4+1] = uint8(clamp(l-c.gu*u-c.gv*v, 255) + .5)
			pix[x*4+2] = uint8(clamp(l+c.bu*u, 255) + .5)
			pix[x*4+3] = 255
		}
	}
}

func chromaSize(r image.YCbCrSubsampleRatio, w, h int) (int, int) {
	switch r {
	case image.YCbCrSubsampleRatio422:
		return (w + 1) / 2, h
	case image.YCbCrSubsampleRatio420:
		return (w + 1) / 2, (h + 1) / 2
	case image.YCbCrSubsampleRatio440:
		return w, (h + 1) / 2
	case image.YCbCrSubsampleRatio411:
		return (w + 3) / 4, h
	case image.YCbCrSubsampleRatio410:
		return (w + 3) / 4, (h + 1) / 2
	}
	return w, h
}

// scaler filters a source of ch interleaved channels down its rows: row(y)
// returns destination row y, reading and filtering horizontally only the
// source rows that the vertical filter of y still needs.
type scaler struct {
	ch     int
	xs, ys []taps
	read   func(y int, line []float32)
	// rows, when set, gives a source row as it is kept, in place of read.
	rows func(y int) []float32
	line []float32
	// Source row r, filtered horizontally, lives in ring[r%len(ring)].
	// Windows only move down, so a slot is reused after its row is needed.
	ring [][]float32
	next int
	acc  []float32
}

func newScaler(ch, sw, sh, w, h int, read func(y int, line []float32)) *scaler {
	return newScalerOf(ch, sw, sh, float64(sw), float64(sh), w, h, read)
}

// newScalerOf is newScaler for a source whose sw×sh pixels cover ex×ey of
// them: the last column and row may be a part of a pixel, as the last block
// of an averaged picture is.
func newScalerOf(ch, sw, sh int, ex, ey float64, w, h int, read func(y int, line []float32)) *scaler {
	s := &scaler{ch: ch, xs: weights(sw, ex, w), ys: weights(sh, ey, h), read: read, line: make([]float32, sw*ch), acc: make([]float32, w*ch)}
	taps := 0
	for _, t := range s.ys {
		taps = max(taps, len(t.w))
	}
	s.ring = make([][]float32, taps)
	for i := range s.ring {
		s.ring[i] = make([]float32, w*ch)
	}
	return s
}

func (s *scaler) row(y int) []float32 {
	ty := s.ys[y]
	for ; s.next < ty.start+len(ty.w); s.next++ {
		line := s.line
		if s.rows != nil {
			line = s.rows(s.next)
		} else {
			s.read(s.next, line)
		}
		out := s.ring[s.next%len(s.ring)]
		if s.ch == 1 {
			for x, tx := range s.xs {
				var v float32
				px := line[tx.start : tx.start+len(tx.w)]
				for i, k := range tx.w {
					v += k * px[i]
				}
				out[x] = v
			}
			continue
		}
		for x, tx := range s.xs {
			var r, g, b, a float32
			px := line[tx.start*4 : (tx.start+len(tx.w))*4]
			for i, k := range tx.w {
				p := px[i*4 : i*4+4]
				r += k * p[0]
				g += k * p[1]
				b += k * p[2]
				a += k * p[3]
			}
			out[x*4], out[x*4+1], out[x*4+2], out[x*4+3] = r, g, b, a
		}
	}
	acc := s.acc
	first := s.ring[ty.start%len(s.ring)]
	k0 := ty.w[0]
	for j, v := range first {
		acc[j] = k0 * v
	}
	for i, k := range ty.w[1:] {
		row := s.ring[(ty.start+1+i)%len(s.ring)]
		row = row[:len(acc)]
		for j, v := range row {
			acc[j] += k * v
		}
	}
	return acc
}

// Fit returns the largest size with the aspect ratio of w×h that is not
// larger than w×h itself and fits into box, or covers it when cover is set.
func Fit(w, h int, box image.Point, cover bool) image.Point {
	if w <= 0 || h <= 0 || box.X <= 0 || box.Y <= 0 {
		return image.Pt(max(w, 0), max(h, 0))
	}
	sx, sy := float64(box.X)/float64(w), float64(box.Y)/float64(h)
	s := min(sx, sy)
	if cover {
		s = max(sx, sy)
	}
	if s >= 1 {
		return image.Pt(w, h)
	}
	return image.Pt(max(1, int(math.Round(float64(w)*s))), max(1, int(math.Round(float64(h)*s))))
}

func clamp(v, hi float32) float32 {
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}

// taps are the normalized filter weights of one destination pixel, applied
// to source pixels start, start+1, ….
type taps struct {
	start int
	w     []float32
}

// weights are the taps of dst pixels over src pixels, which cover extent of
// them.
func weights(src int, extent float64, dst int) []taps {
	scale := extent / float64(dst)
	// Downscaling stretches the kernel over the source; upscaling keeps it.
	stretch := max(scale, 1)
	support := 2 * stretch
	out := make([]taps, dst)
	for i := range out {
		center := (float64(i)+.5)*scale - .5
		lo := max(int(math.Floor(center-support))+1, 0)
		hi := min(int(math.Floor(center+support)), src-1)
		ws := make([]float32, 0, hi-lo+1)
		var sum float64
		for j := lo; j <= hi; j++ {
			k := catmullRom((float64(j) - center) / stretch)
			ws = append(ws, float32(k))
			sum += k
		}
		for j := range ws {
			ws[j] = float32(float64(ws[j]) / sum)
		}
		out[i] = taps{start: lo, w: ws}
	}
	return out
}

func catmullRom(x float64) float64 {
	x = math.Abs(x)
	switch {
	case x < 1:
		return (1.5*x-2.5)*x*x + 1
	case x < 2:
		return ((-.5*x+2.5)*x-4)*x + 2
	}
	return 0
}

// rowReader returns a function filling line with source row y as
// premultiplied RGBA in 0…255. Decoded photos are YCbCr, and decoded PNGs
// RGBA or NRGBA, so those are read directly; anything else goes through At.
func rowReader(src image.Image) func(y int, line []float32) {
	b := src.Bounds()
	switch im := src.(type) {
	case *image.YCbCr:
		return func(y int, line []float32) {
			for x := b.Min.X; x < b.Max.X; x++ {
				yi, ci := im.YOffset(x, y), im.COffset(x, y)
				r, g, bl := color.YCbCrToRGB(im.Y[yi], im.Cb[ci], im.Cr[ci])
				o := (x - b.Min.X) * 4
				line[o], line[o+1], line[o+2], line[o+3] = float32(r), float32(g), float32(bl), 255
			}
		}
	case *image.RGBA:
		return func(y int, line []float32) {
			row := im.Pix[im.PixOffset(b.Min.X, y):]
			for i := range b.Dx() * 4 {
				line[i] = float32(row[i])
			}
		}
	case *image.NRGBA:
		return func(y int, line []float32) {
			row := im.Pix[im.PixOffset(b.Min.X, y):]
			for i := 0; i < b.Dx()*4; i += 4 {
				a := float32(row[i+3])
				line[i] = float32(row[i]) * a / 255
				line[i+1] = float32(row[i+1]) * a / 255
				line[i+2] = float32(row[i+2]) * a / 255
				line[i+3] = a
			}
		}
	}
	return func(y int, line []float32) {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := src.At(x, y).RGBA()
			o := (x - b.Min.X) * 4
			line[o], line[o+1], line[o+2], line[o+3] = float32(r>>8), float32(g>>8), float32(bl>>8), float32(a>>8)
		}
	}
}

// VideoScaler scales decoded video frames, with the alpha of a second frame,
// to premultiplied RGBA, reusing its buffers from one frame to the next.
type VideoScaler struct {
	// Colors says how the frames are read, as their stream tells: the zero value is
	// JPEG's, not a video's.
	Colors Colors
	small  []float32
	sums   []uint32
	opaque *image.YCbCr
	// filter is the filter of the frame before, for sizes filterSize.
	filter     *scaler
	filterSize [6]float64
}

// Resize returns im scaled to w×h, with the luma of alpha as its alpha, or
// opaque when alpha is nil. It never converts the frame at its own size: it
// first averages blocks of 2ⁿ×2ⁿ pixels while that leaves at least the
// destination, then filters what is left with Catmull-Rom as Resize does, and
// converts to RGB only the destination pixels. Premultiplied colour is
// linear in the planes it averages (alpha, alpha×luma, alpha×chroma), so
// only colours out of the RGB gamut come out other than when converting
// first.
func (s *VideoScaler) Resize(im, alpha *image.YCbCr, w, h int) *image.RGBA {
	return s.ResizeInto(image.NewRGBA(image.Rect(0, 0, w, h)), im, alpha)
}

// ResizeInto is Resize into dst, an image at the origin whose size is the
// one to scale to, every pixel of which it writes; it returns dst.
func (s *VideoScaler) ResizeInto(dst *image.RGBA, im, alpha *image.YCbCr) *image.RGBA {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	sw, sh := im.Rect.Dx(), im.Rect.Dy()
	if w <= 0 || h <= 0 || sw <= 0 || sh <= 0 {
		return dst
	}
	fx, fy := blocks(sw, w), blocks(sh, h)
	if alpha == nil && im.SubsampleRatio == image.YCbCrSubsampleRatio420 {
		if w == sw && h == sh {
			if s.Colors == (Colors{}) {
				draw.Draw(dst, dst.Rect, im, im.Rect.Min, draw.Src)
				return dst
			}
			Convert(dst, im, s.Colors)
			return dst
		}
		// Each plane is scaled at its own resolution, chroma at a quarter
		// of the work.
		if fx > 1 || fy > 1 {
			im = s.shrink(im, fx, fy, (sw+fx-1)/fx, (sh+fy-1)/fy)
		}
		resizeYCbCr(dst, im, s.Colors)
		return dst
	}
	c := s.Colors.conversion()
	row := s.premultiplied(im, alpha, fx, fy, w, h)
	for y := range h {
		premultipliedRow(dst.Pix[y*dst.Stride:y*dst.Stride+w*4], row(y), c)
	}
	return dst
}

// blocks is how many pixels of src a block takes, a power of two up to 16,
// so that the sums of blocks fit in 32 bits: blocks are averaged down to
// the destination itself, rather than to twice it, as moving pictures hide
// the softer filter, and video is where the work is.
func blocks(src, dst int) int {
	f := 1
	for f < 16 && src/(f*2) >= dst {
		f *= 2
	}
	return f
}

// premultiplied averages im, with alpha, in blocks of fx×fy and returns
// what gives row y of it scaled to w×h, the rows asked for in order, as
// premultiplied planes: alpha×luma, alpha×chroma from 128, and alpha, in
// 0…255. A row is good until the next one is asked for.
func (s *VideoScaler) premultiplied(im, alpha *image.YCbCr, fx, fy, w, h int) func(y int) []float32 {
	sw, sh := im.Rect.Dx(), im.Rect.Dy()
	bw, bh := (sw+fx-1)/fx, (sh+fy-1)/fy
	s.average(im, alpha, fx, fy, bw, bh)
	if bw == w && bh == h {
		// The blocks are the destination's pixels: nothing is left to filter.
		small := s.small
		return func(y int) []float32 { return small[y*w*4 : (y+1)*w*4] }
	}
	return s.filterFor(bw, bh, float64(sw)/float64(fx), float64(sh)/float64(fy), w, h).row
}

// premultipliedRow converts a row of premultiplied planes (alpha×luma,
// alpha×chroma and alpha, in 0…255) to premultiplied RGBA. Black is
// premultiplied too, and colour stays within its alpha, as Catmull-Rom may
// overshoot.
func premultipliedRow(pix []uint8, acc []float32, c conversion) {
	acc = acc[:len(pix)]
	black := c.black / 255
	for x := 0; x+3 < len(pix); x += 4 {
		p, q := pix[x:x+4:x+4], acc[x:x+4:x+4]
		a := clamp(q[3], 255)
		l, u, v := (q[0]-black*q[3])*c.gain, q[1], q[2]
		p[0] = uint8(clamp(l+c.rv*v, a) + .5)
		p[1] = uint8(clamp(l-c.gu*u-c.gv*v, a) + .5)
		p[2] = uint8(clamp(l+c.bu*u, a) + .5)
		p[3] = uint8(a + .5)
	}
}

// filterFor is the filter of the averaged blocks, bw×bh of them over ex×ey
// (the last ones may be cut by the frame's edge), to w×h. It is kept from
// one frame to the next: its weights are those of the sizes alone.
func (s *VideoScaler) filterFor(bw, bh int, ex, ey float64, w, h int) *scaler {
	size := [6]float64{float64(bw), float64(bh), ex, ey, float64(w), float64(h)}
	if s.filter == nil || s.filterSize != size {
		s.filter = newScalerOf(4, bw, bh, ex, ey, w, h, nil)
		s.filterSize = size
	}
	sc := s.filter
	sc.next = 0
	small := s.small
	sc.rows = func(y int) []float32 { return small[y*bw*4 : (y+1)*bw*4] }
	return sc
}

// shrink averages blocks of fx×fy pixels of every plane of a 4:2:0 frame
// into a frame of bw×bh, reused from one call to the next.
func (s *VideoScaler) shrink(im *image.YCbCr, fx, fy, bw, bh int) *image.YCbCr {
	if s.opaque == nil || s.opaque.Rect.Dx() != bw || s.opaque.Rect.Dy() != bh {
		s.opaque = image.NewYCbCr(image.Rect(0, 0, bw, bh), image.YCbCrSubsampleRatio420)
	}
	out := s.opaque
	sw, sh := im.Rect.Dx(), im.Rect.Dy()
	cw, ch := (sw+1)/2, (sh+1)/2
	s.shrinkPlane(out.Y, out.YStride, bw, bh, im.Y, im.YStride, sw, sh, fx, fy)
	// ceil(ceil(n/2)/f) is ceil(ceil(n/f)/2): the chroma planes line up.
	ow, oh := (bw+1)/2, (bh+1)/2
	s.shrinkPlane(out.Cb, out.CStride, ow, oh, im.Cb, im.CStride, cw, ch, fx, fy)
	s.shrinkPlane(out.Cr, out.CStride, ow, oh, im.Cr, im.CStride, cw, ch, fx, fy)
	return out
}

// shrinkPlane averages blocks of fx×fy samples of src, sw×sh, into dst.
func (s *VideoScaler) shrinkPlane(dst []uint8, dstride, dw, dh int, src []uint8, sstride, sw, sh, fx, fy int) {
	if cap(s.sums) < dw {
		s.sums = make([]uint32, dw)
	}
	sums := s.sums[:dw]
	for by := range dh {
		clear(sums)
		y0, y1 := by*fy, min(by*fy+fy, sh)
		for y := y0; y < y1; y++ {
			row := src[y*sstride : y*sstride+sw]
			for bx := range dw {
				var sum uint32
				for _, v := range row[bx*fx : min(bx*fx+fx, sw)] {
					sum += uint32(v)
				}
				sums[bx] += sum
			}
		}
		out := dst[by*dstride : by*dstride+dw]
		for bx, sum := range sums {
			n := uint32((y1 - y0) * (min(bx*fx+fx, sw) - bx*fx))
			out[bx] = uint8((sum + n/2) / n)
		}
	}
}

// average fills small with bw×bh blocks of fx×fy source pixels, each the
// mean of alpha×luma, alpha×(chroma-128) and alpha, in 0…255.
func (s *VideoScaler) average(im, alpha *image.YCbCr, fx, fy, bw, bh int) {
	sw, sh := im.Rect.Dx(), im.Rect.Dy()
	if cap(s.small) < bw*bh*4 {
		s.small = make([]float32, bw*bh*4)
	}
	s.small = s.small[:bw*bh*4]
	if cap(s.sums) < bw*4 {
		s.sums = make([]uint32, bw*4)
	}
	sums := s.sums[:bw*4]
	cx, cy := chromaShift(im.SubsampleRatio)
	aw, ah := 0, 0
	if alpha != nil {
		aw, ah = min(alpha.Rect.Dx(), sw), min(alpha.Rect.Dy(), sh)
	}
	for by := range bh {
		clear(sums)
		y0, y1 := by*fy, min(by*fy+fy, sh)
		for y := y0; y < y1; y++ {
			luma := im.Y[y*im.YStride : y*im.YStride+sw]
			crow := (y >> cy) * im.CStride
			cb, cr := im.Cb[crow:], im.Cr[crow:]
			var mask []uint8
			if y < ah {
				mask = alpha.Y[y*alpha.YStride : y*alpha.YStride+aw]
			}
			if fx == 1 && fy == 1 && cx == 1 && len(mask) == sw {
				premultiplyRow(s.small[by*bw*4:(by+1)*bw*4], luma, mask, cb, cr)
				continue
			}
			if cx == 1 && fx >= 2 && sw%2 == 0 && len(mask) == sw {
				averagePairs(sums, luma, mask, cb[:sw/2], cr[:sw/2], fx)
				continue
			}
			for bx := range bw {
				x0, x1 := bx*fx, min(bx*fx+fx, sw)
				var sy, su, sv, sa uint32
				for x := x0; x < x1; x++ {
					a := uint32(255)
					if x < len(mask) {
						a = uint32(mask[x])
					}
					sy += a * uint32(luma[x])
					su += a * uint32(cb[x>>cx])
					sv += a * uint32(cr[x>>cx])
					sa += a
				}
				o := bx * 4
				sums[o] += sy
				sums[o+1] += su
				sums[o+2] += sv
				sums[o+3] += sa
			}
		}
		if fx == 1 && fy == 1 && cx == 1 && aw == sw && by < ah {
			continue // premultiplyRow wrote the row.
		}
		rows := y1 - y0
		out := s.small[by*bw*4 : (by+1)*bw*4]
		for bx := range bw {
			n := float32(rows * (min(bx*fx+fx, sw) - bx*fx))
			o := bx * 4
			sa := float32(sums[o+3])
			out[o] = float32(sums[o]) / (255 * n)
			out[o+1] = (float32(sums[o+1]) - 128*sa) / (255 * n)
			out[o+2] = (float32(sums[o+2]) - 128*sa) / (255 * n)
			out[o+3] = sa / n
		}
	}
}

// premultiplyRow writes a row of video with its alpha, the chroma of which
// is half as wide, as blocks of one pixel: alpha×luma, alpha×(chroma-128)
// and alpha, in 0…255.
func premultiplyRow(out []float32, luma, mask, cb, cr []uint8) {
	mask = mask[:len(luma)]
	out = out[:len(luma)*4]
	for x, l := range luma {
		a := float32(mask[x]) * (1.0 / 255)
		o := out[x*4 : x*4+4 : x*4+4]
		o[0] = a * float32(l)
		o[1] = a * float32(int(cb[x>>1])-128)
		o[2] = a * float32(int(cr[x>>1])-128)
		o[3] = float32(mask[x])
	}
}

// averagePairs adds a row of 4:2:0 video with its alpha to the sums of
// blocks fx pixels wide, fx a power of two: each pair of pixels shares a
// chroma sample, which is weighed by their alpha together.
func averagePairs(sums []uint32, luma, mask, cb, cr []uint8, fx int) {
	shift := bits.TrailingZeros(uint(fx)) - 1
	for c := range cb {
		x := c * 2
		a0, a1 := uint32(mask[x]), uint32(mask[x+1])
		la := a0 + a1
		o := (c >> shift) * 4
		s := sums[o : o+4 : o+4]
		s[0] += a0*uint32(luma[x]) + a1*uint32(luma[x+1])
		s[1] += la * uint32(cb[c])
		s[2] += la * uint32(cr[c])
		s[3] += la
	}
}

// chromaShift returns how far the chroma planes of r are shifted down from
// luma, horizontally and vertically.
func chromaShift(r image.YCbCrSubsampleRatio) (x, y uint) {
	switch r {
	case image.YCbCrSubsampleRatio422:
		return 1, 0
	case image.YCbCrSubsampleRatio420:
		return 1, 1
	case image.YCbCrSubsampleRatio440:
		return 0, 1
	case image.YCbCrSubsampleRatio411:
		return 2, 0
	case image.YCbCrSubsampleRatio410:
		return 2, 1
	}
	return 0, 0
}
