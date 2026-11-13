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
	"encoding/binary"
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
	resizePlanes(dst, im, colors, nil, false)
}

// planeScalers are the filters of the three planes of frames of one size
// scaled to one size, kept from one frame to the next.
type planeScalers struct {
	size    [7]int
	filters [3]*scaler
}

// resizePlanes is resizeYCbCr with the filters of kept, when it is not nil,
// for the next frame of the same sizes; linear, the filters take the two
// samples a pixel lies between, not all that Catmull-Rom spans.
func resizePlanes(dst *image.RGBA, im *image.YCbCr, colors Colors, kept *planeScalers, linear bool) {
	c := colors.conversion()
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	sw, sh := im.Rect.Dx(), im.Rect.Dy()
	cw, ch := chromaSize(im.SubsampleRatio, sw, sh)
	size := [7]int{sw, sh, cw, ch, w, h, 0}
	if linear {
		size[6] = 1
	}
	fresh := kept == nil || kept.size != size
	n := 0
	plane := func(pix []uint8, stride, pw, ph int) *scaler {
		read := func(y int, line []float32) {
			row := pix[y*stride : y*stride+pw]
			for i, v := range row {
				line[i] = float32(v)
			}
		}
		n++
		if fresh {
			sc := newScaler(1, pw, ph, w, h, read)
			if linear {
				sc.xs, sc.ys = linearWeights(pw, w), linearWeights(ph, h)
			}
			if kept != nil {
				kept.filters[n-1] = sc
			}
			return sc
		}
		sc := kept.filters[n-1]
		sc.next, sc.read = 0, read
		return sc
	}
	ys := plane(im.Y, im.YStride, sw, sh)
	cbs := plane(im.Cb, im.CStride, cw, ch)
	crs := plane(im.Cr, im.CStride, cw, ch)
	if kept != nil {
		kept.size = size
	}
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

// linearWeights are the taps of dst pixels over src, each between the two
// samples it lies between: for what blocks have averaged already, down to
// less than twice dst, where every sample still counts.
func linearWeights(src, dst int) []taps {
	scale := float64(src) / float64(dst)
	out := make([]taps, dst)
	for i := range out {
		center := (float64(i)+.5)*scale - .5
		lo := min(max(int(math.Floor(center)), 0), src-1)
		hi := min(lo+1, src-1)
		k := float32(min(max(center-float64(lo), 0), 1))
		if hi == lo {
			out[i] = taps{start: lo, w: []float32{1}}
			continue
		}
		out[i] = taps{start: lo, w: []float32{1 - k, k}}
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
	// Rough scales video without alpha cheaper: what is left after the
	// blocks are averaged is filtered between two samples, not with
	// Catmull-Rom. For pictures shown several times smaller than they
	// are, where it is not seen, and the filter was half of the work.
	Rough  bool
	small  []float32
	sums   []uint32
	lanes  []uint64
	opaque *image.YCbCr
	// filter is the filter of the frame before, for sizes filterSize.
	filter     *scaler
	filterSize [6]float64
	// planes are the filters of video without alpha.
	planes planeScalers
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
		resizePlanes(dst, im, s.Colors, &s.planes, s.Rough && (fx > 1 || fy > 1))
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
		// Eight samples of a row are added at once, in pairs, to four sums
		// of 16 bits in a word: the 16 rows of a block at most do not fill
		// them. The words are taken apart into the blocks' sums after.
		words := 0
		if fx > 1 {
			words = sw / 8
		}
		if cap(s.lanes) < words {
			s.lanes = make([]uint64, words)
		}
		lanes := s.lanes[:words]
		clear(lanes)
		for y := y0; y < y1; y++ {
			row := src[y*sstride : y*sstride+sw]
			for i := range lanes {
				v := binary.LittleEndian.Uint64(row[i*8 : i*8+8])
				lanes[i] += v&0x00ff00ff00ff00ff + v>>8&0x00ff00ff00ff00ff
			}
			for x := words * 8; x < sw; x++ {
				sums[x/fx] += uint32(row[x])
			}
		}
		// A sum of a word is of two samples: shift is from its number to
		// its block's.
		shift := bits.TrailingZeros(uint(fx)) - 1
		for i, v := range lanes {
			p := i * 4
			sums[p>>shift] += uint32(v & 0xffff)
			sums[(p+1)>>shift] += uint32(v >> 16 & 0xffff)
			sums[(p+2)>>shift] += uint32(v >> 32 & 0xffff)
			sums[(p+3)>>shift] += uint32(v >> 48)
		}
		out := dst[by*dstride : by*dstride+dw]
		// whole blocks, of a size that is a power of two, are divided by a
		// shift; the last one, cut by the plane's edge, as it comes.
		whole := 0
		if rows := y1 - y0; rows&(rows-1) == 0 {
			whole = sw / fx
			n := uint32(rows * fx)
			by := bits.TrailingZeros32(n)
			for bx, sum := range sums[:whole] {
				out[bx] = uint8((sum + n/2) >> by)
			}
		}
		for bx := whole; bx < dw; bx++ {
			n := uint32((y1 - y0) * (min(bx*fx+fx, sw) - bx*fx))
			out[bx] = uint8((sums[bx] + n/2) / n)
		}
	}
}

// sum8 is the sum of the eight bytes of v, and sum4 of the four: added in
// pairs, then the pairs, within the word.
func sum8(v uint64) uint32 {
	v = v&0x00ff00ff00ff00ff + v>>8&0x00ff00ff00ff00ff
	v = v&0x0000ffff0000ffff + v>>16&0x0000ffff0000ffff
	return uint32(v) + uint32(v>>32)
}

func sum4(v uint32) uint32 {
	v = v&0x00ff00ff + v>>8&0x00ff00ff
	return v&0xffff + v>>16
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
	// pairs is how many pairs of pixels a block is wide.
	pairs := fx / 2
	luma, mask, cr = luma[:len(cb)*2], mask[:len(cb)*2], cr[:len(cb)]
	for c, o := 0, 0; c < len(cb); o += 4 {
		end := min(c+pairs, len(cb))
		// The sums of a block are kept out of memory until it is whole.
		var sy, su, sv, sa uint32
		for c < end {
			x := c * 2
			// Much of a sticker is clear, and most of the rest opaque:
			// eight pixels of either, or four, are told and added at once.
			if c+4 <= end {
				switch binary.LittleEndian.Uint64(mask[x:]) {
				case 0:
					c += 4
					continue
				case 1<<64 - 1:
					sy += 255 * sum8(binary.LittleEndian.Uint64(luma[x:]))
					su += 510 * sum4(binary.LittleEndian.Uint32(cb[c:]))
					sv += 510 * sum4(binary.LittleEndian.Uint32(cr[c:]))
					sa += 8 * 255
					c += 4
					continue
				}
			} else if c+2 <= end {
				switch binary.LittleEndian.Uint32(mask[x:]) {
				case 0:
					c += 2
					continue
				case 1<<32 - 1:
					sy += 255 * sum4(binary.LittleEndian.Uint32(luma[x:]))
					su += 510 * (uint32(cb[c]) + uint32(cb[c+1]))
					sv += 510 * (uint32(cr[c]) + uint32(cr[c+1]))
					sa += 4 * 255
					c += 2
					continue
				}
			}
			a0, a1 := uint32(mask[x]), uint32(mask[x+1])
			la := a0 + a1
			sy += a0*uint32(luma[x]) + a1*uint32(luma[x+1])
			su += la * uint32(cb[c])
			sv += la * uint32(cr[c])
			sa += la
			c++
		}
		s := sums[o : o+4 : o+4]
		s[0] += sy
		s[1] += su
		s[2] += sv
		s[3] += sa
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
