// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import (
	"image"
	"image/color"
	"math"
	"slices"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"

	"komarugram/pkg/resample"
)

// Everything here blends in the values the pixels hold, without taking them to
// linear light, on purpose. FFmpeg's overlay does so, and Telegram Desktop's
// editor, so the marker's 35% is what it is there, and a photo, a video and the
// view of them come out the same. Gio blends in linear light, which is why the
// view is a picture drawn here rather than the same things drawn by Gio.

// Rasters gives a text as a picture, of a font of px pixels or near it; nil
// while it has none. The text is drawn stretched to its element's box.
type Rasters func(t *Text, px float64) *image.NRGBA

// target is what elements are drawn on: a picture, or a layer to lay over one.
type target interface {
	// paint lays col over what cov covers.
	paint(cov *image.Alpha, col color.NRGBA)
	// picture lays src, premultiplied, over what it covers.
	picture(src *image.RGBA)
	// blur blurs by radius what is drawn so far, where mask covers.
	blur(mask *image.Alpha, radius int)
}

// flat is a target that is the picture itself, opaque.
type flat struct{ img *image.RGBA }

func (t flat) paint(cov *image.Alpha, col color.NRGBA) {
	r := cov.Rect.Intersect(t.img.Rect)
	ca := uint32(col.A)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := cov.Pix[cov.PixOffset(r.Min.X, y):]
		dst := t.img.Pix[t.img.PixOffset(r.Min.X, y):]
		for x := range r.Dx() {
			a := (uint32(src[x])*ca + 127) / 255
			if a == 0 {
				continue
			}
			d := dst[x*4 : x*4+4 : x*4+4]
			d[0] = mix(d[0], col.R, a)
			d[1] = mix(d[1], col.G, a)
			d[2] = mix(d[2], col.B, a)
		}
	}
}

func (t flat) picture(src *image.RGBA) {
	r := src.Rect.Intersect(t.img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		s := src.Pix[src.PixOffset(r.Min.X, y):]
		dst := t.img.Pix[t.img.PixOffset(r.Min.X, y):]
		for x := range r.Dx() {
			p := s[x*4 : x*4+4 : x*4+4]
			if p[3] == 0 {
				continue
			}
			d := dst[x*4 : x*4+4 : x*4+4]
			rest := 255 - uint32(p[3])
			d[0] = uint8(uint32(p[0]) + (uint32(d[0])*rest+127)/255)
			d[1] = uint8(uint32(p[1]) + (uint32(d[1])*rest+127)/255)
			d[2] = uint8(uint32(p[2]) + (uint32(d[2])*rest+127)/255)
		}
	}
}

func (t flat) blur(mask *image.Alpha, radius int) {
	if radius <= 0 {
		return
	}
	// As far around as the two passes reach.
	r := mask.Rect.Inset(-2*radius - 1).Intersect(t.img.Rect)
	if r.Empty() {
		return
	}
	blurred := image.NewRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(blurred.Pix[blurred.PixOffset(r.Min.X, y):blurred.PixOffset(r.Min.X, y)+4*r.Dx()], t.img.Pix[t.img.PixOffset(r.Min.X, y):])
	}
	boxBlur(blurred, radius)
	m := mask.Rect.Intersect(t.img.Rect)
	for y := m.Min.Y; y < m.Max.Y; y++ {
		src := mask.Pix[mask.PixOffset(m.Min.X, y):]
		b := blurred.Pix[blurred.PixOffset(m.Min.X, y):]
		dst := t.img.Pix[t.img.PixOffset(m.Min.X, y):]
		for x := range m.Dx() {
			a := uint32(src[x])
			if a == 0 {
				continue
			}
			d := dst[x*4 : x*4+4 : x*4+4]
			d[0] = mix(d[0], b[x*4], a)
			d[1] = mix(d[1], b[x*4+1], a)
			d[2] = mix(d[2], b[x*4+2], a)
		}
	}
}

// mix is d with share a of 255 of s.
func mix(d, s uint8, a uint32) uint8 {
	return uint8((uint32(d)*(255-a) + uint32(s)*a + 127) / 255)
}

// boxBlur blurs img by a box of 2×radius+1 pixels, twice each way, as
// FFmpeg's boxblur=radius:2.
func boxBlur(img *image.RGBA, radius int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == 0 || h == 0 {
		return
	}
	line := make([]uint8, 4*max(w, h))
	for range 2 {
		for y := range h {
			boxLine(img.Pix[y*img.Stride:], 4, w, radius, line)
		}
		for x := range w {
			boxLine(img.Pix[x*4:], img.Stride, h, radius, line)
		}
	}
}

// boxLine blurs the n pixels of pix a stride apart.
func boxLine(pix []uint8, stride, n, radius int, line []uint8) {
	for i := range n {
		copy(line[i*4:i*4+4], pix[i*stride:i*stride+4])
	}
	window := uint32(2*radius + 1)
	for c := range 3 {
		sum := uint32(0)
		at := func(i int) uint32 { return uint32(line[min(n-1, max(0, i))*4+c]) }
		for i := -radius; i <= radius; i++ {
			sum += at(i)
		}
		for i := range n {
			pix[i*stride+c] = uint8((sum + window/2) / window)
			sum += at(i+radius+1) - at(i-radius)
		}
	}
}

// layer is a target that keeps what is drawn apart from the picture, for
// FFmpeg to lay over each frame.
type layer struct {
	size  image.Point
	over  *image.NRGBA
	steps []Step
}

// Step is one pass over the picture, of the passes Layers gives.
type Step struct {
	// Over is laid over it, unless nil.
	Over *image.NRGBA
	// Blur is where it is blurred by Radius, unless nil.
	Blur   *image.Alpha
	Radius int
}

func (t *layer) canvas() *image.NRGBA {
	if t.over == nil {
		t.over = image.NewNRGBA(image.Rectangle{Max: t.size})
	}
	return t.over
}

func (t *layer) paint(cov *image.Alpha, col color.NRGBA) {
	over := t.canvas()
	r := cov.Rect.Intersect(over.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := cov.Pix[cov.PixOffset(r.Min.X, y):]
		dst := over.Pix[over.PixOffset(r.Min.X, y):]
		for x := range r.Dx() {
			if a := (uint32(src[x])*uint32(col.A) + 127) / 255; a > 0 {
				under(dst[x*4:x*4+4:x*4+4], col.R, col.G, col.B, a)
			}
		}
	}
}

func (t *layer) picture(src *image.RGBA) {
	over := t.canvas()
	r := src.Rect.Intersect(over.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		s := src.Pix[src.PixOffset(r.Min.X, y):]
		dst := over.Pix[over.PixOffset(r.Min.X, y):]
		for x := range r.Dx() {
			p := s[x*4 : x*4+4 : x*4+4]
			a := uint32(p[3])
			if a == 0 {
				continue
			}
			under(dst[x*4:x*4+4:x*4+4], uint8(min(255, uint32(p[0])*255/a)), uint8(min(255, uint32(p[1])*255/a)), uint8(min(255, uint32(p[2])*255/a)), a)
		}
	}
}

// under lays a color of alpha a over d, a pixel with alpha of its own, so that
// d laid over anything is as the color laid over d laid over it.
func under(d []uint8, r, g, b uint8, a uint32) {
	da := uint32(d[3]) * (255 - a)
	out := a*255 + da
	if out == 0 {
		return
	}
	over := func(s, o uint8) uint8 { return uint8((uint32(s)*a*255 + uint32(o)*da + out/2) / out) }
	d[0], d[1], d[2], d[3] = over(r, d[0]), over(g, d[1]), over(b, d[2]), uint8((out+127)/255)
}

func (t *layer) blur(mask *image.Alpha, radius int) {
	if t.over != nil {
		t.steps = append(t.steps, Step{Over: t.over})
		t.over = nil
	}
	if n := len(t.steps); n > 0 && t.steps[n-1].Blur != nil {
		// Strokes of blur in a row are one blur, as they would blur nothing
		// but each other otherwise.
		merge(t.steps[n-1].Blur, mask)
		return
	}
	full := image.NewAlpha(image.Rectangle{Max: t.size})
	merge(full, mask)
	t.steps = append(t.steps, Step{Blur: full, Radius: radius})
}

// merge adds what src covers to dst.
func merge(dst, src *image.Alpha) {
	r := src.Rect.Intersect(dst.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		d := dst.Pix[dst.PixOffset(r.Min.X, y):]
		s := src.Pix[src.PixOffset(r.Min.X, y):]
		for x := range r.Dx() {
			d[x] = max(d[x], s[x])
		}
	}
}

// painter draws the elements of a list on one canvas. An eraser takes from the
// strokes under it, whenever they are drawn: its cover is kept for them.
type painter struct {
	all     []*Element
	canvas  Canvas
	rasters Rasters
	blurred int
	cuts    map[int]*image.Alpha
}

func newPainter(all []*Element, c Canvas, rasters Rasters, source image.Point) *painter {
	return &painter{all: all, canvas: c, rasters: rasters, blurred: max(1, int(math.Round(float64(BlurRadius(source))*c.Scale)))}
}

// cut is the cover of the eraser's stroke at i.
func (p *painter) cut(i int) *image.Alpha {
	if cov, ok := p.cuts[i]; ok {
		return cov
	}
	if p.cuts == nil {
		p.cuts = map[int]*image.Alpha{}
	}
	cov := strokeOutline(p.all[i].Stroke, p.canvas).cover(p.canvas)
	p.cuts[i] = cov
	return cov
}

// draw draws the elements from up to to on t.
func (p *painter) draw(t target, from, to int) {
	for i := from; i < to; i++ {
		e := p.all[i]
		switch {
		case e.Stroke != nil:
			p.stroke(t, i)
		case e.Shape != nil:
			if cov := shapeOutline(e, p.canvas).cover(p.canvas); cov != nil {
				t.paint(cov, e.Shape.Color)
			}
		case e.Sticker != nil:
			p.image(t, e, e.Sticker.Image)
		case e.Text != nil:
			if p.rasters != nil {
				p.image(t, e, p.rasters(e.Text, e.Text.Size*p.canvas.Scale))
			}
		}
	}
}

func (p *painter) stroke(t target, i int) {
	e := p.all[i]
	s := e.Stroke
	if s.Tool == Eraser || len(s.Points) == 0 {
		return
	}
	cov := strokeOutline(s, p.canvas).cover(p.canvas)
	if cov == nil {
		return
	}
	for j := i + 1; j < len(p.all); j++ {
		if o := p.all[j]; o.Stroke != nil && o.Stroke.Tool == Eraser && o.erases(e) {
			erase(cov, p.cut(j))
		}
	}
	col := s.Color
	switch s.Tool {
	case Blur:
		t.blur(cov, p.blurred)
		return
	case Marker:
		col.A = uint8(math.Round(float64(col.A) * markerOpacity))
	}
	t.paint(cov, col)
}

// image draws src in e's box.
func (p *painter) image(t target, e *Element, src *image.NRGBA) {
	if src == nil {
		return
	}
	b := src.Bounds()
	if b.Empty() || e.Width <= 0 || e.Height <= 0 {
		return
	}
	// A pixel of src, then in its box, in the picture, on the canvas.
	sx, sy := e.Width/float64(b.Dx())*p.canvas.Scale, e.Height/float64(b.Dy())*p.canvas.Scale
	if e.Flipped {
		sx = -sx
	}
	sin, cos := math.Sincos(e.Rotation * math.Pi / 180)
	center := p.canvas.To(e.Center)
	m := f64.Aff3{
		cos * sx, -sin * sy, center.X,
		sin * sx, cos * sy, center.Y,
	}
	// Around the middle of src.
	mx, my := float64(b.Min.X)+float64(b.Dx())/2, float64(b.Min.Y)+float64(b.Dy())/2
	m[2] -= m[0]*mx + m[1]*my
	m[5] -= m[3]*mx + m[4]*my
	// Where it comes out.
	lo, hi := Point{math.Inf(1), math.Inf(1)}, Point{math.Inf(-1), math.Inf(-1)}
	for _, c := range [4]Point{{float64(b.Min.X), float64(b.Min.Y)}, {float64(b.Max.X), float64(b.Min.Y)}, {float64(b.Max.X), float64(b.Max.Y)}, {float64(b.Min.X), float64(b.Max.Y)}} {
		q := Point{m[0]*c.X + m[1]*c.Y + m[2], m[3]*c.X + m[4]*c.Y + m[5]}
		lo.X, lo.Y = min(lo.X, q.X), min(lo.Y, q.Y)
		hi.X, hi.Y = max(hi.X, q.X), max(hi.Y, q.Y)
	}
	const far = 1 << 24
	r := image.Rect(int(max(-far, math.Floor(lo.X))), int(max(-far, math.Floor(lo.Y))), int(min(far, math.Ceil(hi.X))), int(min(far, math.Ceil(hi.Y))))
	r = r.Intersect(image.Rectangle{Max: p.canvas.Size()})
	if r.Empty() {
		return
	}
	out := image.NewRGBA(r)
	xdraw.CatmullRom.Transform(out, m, src, b, xdraw.Src, nil)
	t.picture(out)
}

// Flatten is the picture of a scene as a canvas shows it, base being what the
// canvas has of the picture, opaque.
func Flatten(base *image.RGBA, elements []*Element, c Canvas, rasters Rasters, source image.Point) *image.RGBA {
	out := image.NewRGBA(base.Rect)
	copy(out.Pix, base.Pix)
	newPainter(elements, c, rasters, source).draw(flat{out}, 0, len(elements))
	return out
}

// Layers are the passes that make what the scene draws over a picture of its
// size, for FFmpeg to make over each frame of a video.
func Layers(s *Scene, rasters Rasters) []Step {
	c := CanvasOf(image.Rectangle{Max: s.Size}, 1)
	l := &layer{size: s.Size}
	newPainter(s.Elements, c, rasters, s.Size).draw(l, 0, len(s.Elements))
	if l.over != nil {
		l.steps = append(l.steps, Step{Over: l.over})
	}
	return l.steps
}

// BaseOf is what a canvas has of a picture, whose pixels picture has scale of.
func BaseOf(picture *image.RGBA, scale float64, c Canvas) *image.RGBA {
	part := image.Rect(
		int(math.Floor(float64(c.Rect.Min.X)*scale)), int(math.Floor(float64(c.Rect.Min.Y)*scale)),
		int(math.Ceil(float64(c.Rect.Max.X)*scale)), int(math.Ceil(float64(c.Rect.Max.Y)*scale)),
	).Intersect(picture.Rect)
	size := c.Size()
	sub, ok := picture.SubImage(part).(*image.RGBA)
	if !ok || part.Empty() {
		return image.NewRGBA(image.Rectangle{Max: size})
	}
	if part.Size() == size {
		// The rows are moved, not resampled, at the picture's own size.
		out := image.NewRGBA(image.Rectangle{Max: size})
		for y := range size.Y {
			copy(out.Pix[y*out.Stride:(y+1)*out.Stride], sub.Pix[y*sub.Stride:])
		}
		return out
	}
	return resample.Resize(sub, size.X, size.Y)
}

// Preview draws a scene for the editor's view over and over, redrawing only
// what changed: the elements under the one that does are kept drawn, the way
// the picture is, so a long list does not slow a stroke down.
type Preview struct {
	source image.Point
	canvas Canvas
	base   *image.RGBA
	// stable is base with kept drawn; out what the last Render made of it.
	stable *image.RGBA
	kept   []*Element
	out    *image.RGBA
	drawn  []*Element
	fresh  bool
}

// SetBase starts over on what a canvas has of a picture of source.
func (p *Preview) SetBase(base *image.RGBA, c Canvas, source image.Point) {
	*p = Preview{source: source, canvas: c, base: base}
}

// Canvas is the canvas of the view.
func (p *Preview) Canvas() Canvas { return p.canvas }

// Ready reports whether there is a picture to draw on.
func (p *Preview) Ready() bool { return p.base != nil }

// Invalidate makes the next Render draw all again: a text's picture came.
func (p *Preview) Invalidate() {
	if p.base != nil {
		p.stable, p.kept, p.fresh = nil, nil, false
	}
}

// Release lets go of the pictures.
func (p *Preview) Release() { *p = Preview{} }

// Render is the view of elements. changed tells that the picture is another
// than the last one Render returned, though it may be the same memory. settled
// says no element is about to change again, which keeps it for the next.
func (p *Preview) Render(elements []*Element, settled bool, rasters Rasters) (img *image.RGBA, changed bool) {
	if p.base == nil {
		return nil, false
	}
	if p.fresh && slices.Equal(p.drawn, elements) {
		return p.out, false
	}
	if p.stable == nil {
		p.stable = image.NewRGBA(p.base.Rect)
		copy(p.stable.Pix, p.base.Pix)
	}
	if p.out == nil {
		p.out = image.NewRGBA(p.base.Rect)
	}
	painter := newPainter(elements, p.canvas, rasters, p.source)
	// What an eraser above reaches is not drawn for good yet.
	limit := reusable(elements)
	common := 0
	for common < len(p.kept) && common < len(elements) && p.kept[common] == elements[common] {
		common++
	}
	common = min(common, limit)
	if common < len(p.kept) {
		copy(p.stable.Pix, p.base.Pix)
		painter.draw(flat{p.stable}, 0, common)
		p.kept = slices.Clone(elements[:common])
	}
	copy(p.out.Pix, p.stable.Pix)
	painter.draw(flat{p.out}, common, len(elements))
	if settled && limit > common {
		// All up to where an eraser reaches is good for the next one, as it
		// is drawn: moved, not copied, the two pictures changing places.
		if limit == len(elements) {
			p.stable, p.out = p.out, p.stable
			p.kept = slices.Clone(elements)
			copy(p.out.Pix, p.stable.Pix)
		} else {
			painter.draw(flat{p.stable}, common, limit)
			p.kept = slices.Clone(elements[:limit])
		}
	}
	p.drawn, p.fresh = slices.Clone(elements), true
	return p.out, true
}

// reusable is how many elements from the first no eraser above them reaches.
func reusable(elements []*Element) int {
	limit := len(elements)
	for j, e := range elements {
		if e.Stroke == nil || e.Stroke.Tool != Eraser {
			continue
		}
		for i := range min(j, limit) {
			if o := elements[i]; o.Stroke != nil && o.Stroke.Tool != Eraser && e.erases(o) {
				limit = i
				break
			}
		}
	}
	return limit
}

// Straighten is img, as the GPU gives a picture drawn on nothing, with colors
// of their own. Gio blends in linear light and stores that in sRGB: a pixel is
// its color in linear light times its alpha, encoded. Its alpha is as it was.
func Straighten(img *image.RGBA) *image.NRGBA {
	out := image.NewNRGBA(img.Rect)
	for i := 0; i+3 < len(img.Pix); i += 4 {
		a := img.Pix[i+3]
		if a == 0 {
			continue
		}
		out.Pix[i+3] = a
		if a == 255 {
			copy(out.Pix[i:i+3], img.Pix[i:i+3])
			continue
		}
		share := float64(a) / 255
		for c := range 3 {
			out.Pix[i+c] = encodeSRGB(min(1, decodeSRGB[img.Pix[i+c]]/share))
		}
	}
	return out
}

// decodeSRGB is each value of sRGB in linear light.
var decodeSRGB = func() (table [256]float64) {
	for i := range table {
		v := float64(i) / 255
		if v <= .04045 {
			table[i] = v / 12.92
		} else {
			table[i] = math.Pow((v+.055)/1.055, 2.4)
		}
	}
	return table
}()

// encodeSRGB is a value in linear light in sRGB.
func encodeSRGB(v float64) uint8 {
	if v <= .0031308 {
		v *= 12.92
	} else {
		v = 1.055*math.Pow(v, 1/2.4) - .055
	}
	return uint8(v*255 + .5)
}
