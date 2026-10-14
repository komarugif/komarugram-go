// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/draw"
	"math"
	"sync"
	"time"

	"komarugram/pkg/resample"

	"gioui.org/f32"
	"gioui.org/layout"
)

// Motion blur, as a camera's: a picture moving while the shutter is open
// leaves the average of where it was, a streak fading at both ends. It is
// computed, not faked: the frames a picture passes through are averaged in
// premultiplied colors, which the GPU's blending then lays over what is
// under it exactly as their average would be. Drawing the copies one over
// the other, half transparent, would not: blending is not linear in them,
// and leaves the middle of the streak see-through.

// motionShutter is how long the shutter is open: half a frame at 60 frames
// a second, as a film camera's 180° shutter.
const motionShutter = time.Second / 120

// maxBlurSamples bounds the pictures averaged for one streak; they are at
// most a pixel apart below it.
const maxBlurSamples = 48

var blurScratch = sync.Pool{New: func() any { return new([]uint16) }}

// motionTrack follows where a picture is from frame to frame, for the
// streak its motion leaves.
type motionTrack struct {
	at   f32.Point
	when time.Time
	seen bool
}

// Streak is how far the picture at at moved while the shutter was open,
// ending now: what it moved since the frame before, in the part of that
// frame's time the shutter takes. A first frame, or one after the picture
// stood still, has none.
func (m *motionTrack) Streak(gtx layout.Context, at f32.Point) f32.Point {
	last, when, seen := m.at, m.when, m.seen
	m.at, m.when, m.seen = at, gtx.Now, true
	dt := gtx.Now.Sub(when)
	if !seen || dt <= 0 {
		return f32.Point{}
	}
	open := min(1, float32(motionShutter)/float32(dt))
	return at.Sub(last).Mul(open)
}

// Reset forgets where the picture was: its next frame has no streak.
func (m *motionTrack) Reset() { *m = motionTrack{} }

// moving tells whether a streak is long enough to show.
func moving(streak f32.Point) bool {
	return math.Hypot(float64(streak.X), float64(streak.Y)) >= 1
}

// blurSprite is src, moved by streak while the shutter was open: the
// average of src at evenly spaced points from -streak to where it is. The
// result is larger than src by the streak; origin is where its corner lies
// from src's.
func blurSprite(src image.Image, streak f32.Point) (out *image.RGBA, origin image.Point) {
	b := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok || rgba.Rect.Min != (image.Point{}) {
		rgba = image.NewRGBA(image.Rectangle{Max: b.Size()})
		draw.Draw(rgba, rgba.Rect, src, b.Min, draw.Src)
	}
	w, h := rgba.Rect.Dx(), rgba.Rect.Dy()
	length := math.Hypot(float64(streak.X), float64(streak.Y))
	n := min(maxBlurSamples, int(math.Ceil(length))+1)
	if n < 2 {
		return rgba, image.Point{}
	}
	// The samples lie on whole pixels, from the start of the streak.
	shifts := make([]image.Point, n)
	minX, minY, maxX, maxY := 0, 0, 0, 0
	for i := range shifts {
		t := float32(i)/float32(n-1) - 1
		s := image.Pt(int(math.Round(float64(streak.X*t))), int(math.Round(float64(streak.Y*t))))
		shifts[i] = s
		minX, minY = min(minX, s.X), min(minY, s.Y)
		maxX, maxY = max(maxX, s.X), max(maxY, s.Y)
	}
	origin = image.Pt(minX, minY)
	size := image.Pt(w+maxX-minX, h+maxY-minY)
	// maxBlurSamples of 255 fit in 16 bits, half the memory of 32 to go
	// through; the sums are kept for the next streak, not made again
	// every frame of a motion.
	scratch := blurScratch.Get().(*[]uint16)
	defer blurScratch.Put(scratch)
	if cap(*scratch) < size.X*size.Y*4 {
		*scratch = make([]uint16, size.X*size.Y*4)
	}
	sum := (*scratch)[:size.X*size.Y*4]
	clear(sum)
	for _, s := range shifts {
		ox, oy := s.X-minX, s.Y-minY
		for y := 0; y < h; y++ {
			src := rgba.Pix[y*rgba.Stride : y*rgba.Stride+w*4]
			dst := sum[((y+oy)*size.X+ox)*4:][:len(src)]
			for i, v := range src {
				dst[i] += uint16(v)
			}
		}
	}
	out = image.NewRGBA(image.Rectangle{Max: size})
	recip := blurReciprocal(uint32(n))
	for i, v := range sum {
		out.Pix[i] = blurAverage(uint32(v), uint32(n), recip)
	}
	return out, origin
}

// blurReciprocal is what blurAverage multiplies by for n samples.
func blurReciprocal(n uint32) uint32 { return (1<<24 + n - 1) / n }

// blurAverage is sum of n samples divided by n, rounded: by a
// multiplication rather than a division, which every channel of every
// pixel of a streak takes.
func blurAverage(sum, n, recip uint32) uint8 {
	return uint8(((sum + n/2) * recip) >> 24)
}

// roundedSprite is src covering a square of side px, its middle, cut to
// the avatars' shape with corners of radius: as an avatar draws a photo.
func roundedSprite(src image.Image, px, radius int) *image.RGBA {
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(b.Min).Add(image.Pt((b.Dx()-side)/2, (b.Dy()-side)/2))
	if sub, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		src = sub.SubImage(crop)
	}
	out := resample.Resize(src, px, px)
	r := float64(radius)
	for y := 0; y < px; y++ {
		for x := 0; x < px; x++ {
			// How much of the pixel the shape covers, by the distance of its
			// center from the shape's edge.
			cx, cy := float64(x)+.5, float64(y)+.5
			dx := math.Max(0, math.Max(r-cx, cx-(float64(px)-r)))
			dy := math.Max(0, math.Max(r-cy, cy-(float64(px)-r)))
			cover := 1.0
			if dx > 0 && dy > 0 {
				cover = math.Max(0, math.Min(1, r-math.Hypot(dx, dy)+.5))
			}
			if cover == 1 {
				continue
			}
			// Premultiplied, every channel takes the cover.
			i := out.PixOffset(x, y)
			for c := range 4 {
				out.Pix[i+c] = uint8(float64(out.Pix[i+c])*cover + .5)
			}
		}
	}
	return out
}
