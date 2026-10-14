// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"math"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// downloadRing draws where a download is, as Telegram Desktop's and
// materialgram's: in a circle, an arc as long as the part received over a
// faint track, around the mark of what a click does, which cancels it while
// it goes. A download of unknown size turns a short arc round. Each row
// keeps its own, for the arc to grow smoothly between frames.
type downloadRing struct {
	progress wdk.FloatTween
	// last is the progress drawn last: a smaller one starts a download
	// anew, which grows from nothing rather than going back.
	last float32
}

// layout draws the ring diameter wide, in fg: with the arc while active,
// progress of the download from 0 to 1, below 0 when its size is not
// known; mark in the middle.
func (d *downloadRing) layout(gtx layout.Context, diameter int, active bool, progress float32, mark wdk.IconWidget, fg token.MatColor, animate bool) layout.Dimensions {
	size := image.Pt(diameter, diameter)
	icon := diameter / 2
	offset(gtx, image.Pt((diameter-icon)/2, (diameter-icon)/2), func(gtx layout.Context) layout.Dimensions {
		return exact(gtx, image.Pt(icon, icon), func(gtx layout.Context) layout.Dimensions {
			return mark(gtx, fg)
		})
	})
	if !active {
		d.reset()
		return layout.Dimensions{Size: size}
	}
	width := float32(max(2, gtx.Dp(2.5)))
	inset := float32(gtx.Dp(3))
	ink := fg.AsNRGBA()
	track := ink
	track.A = uint8(float32(track.A) * .3)
	arc(gtx, diameter, inset, width, 0, 2*math.Pi, track)
	if progress < 0 {
		// A quarter of the ring turns once in 1.4 s.
		start := -math.Pi / 2
		if animate {
			start += float64(gtx.Now.UnixMilli()%1400) * 2 * math.Pi / 1400
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
		}
		arc(gtx, diameter, inset, width, start, math.Pi/2, ink)
		return layout.Dimensions{Size: size}
	}
	progress = min(progress, 1)
	if progress < d.last {
		d.progress = wdk.FloatTween{}
	}
	d.last = progress
	shown := progress
	if animate {
		shown = d.progress.Animate(gtx, progress)
	}
	// A sliver shows that it started.
	arc(gtx, diameter, inset, width, -math.Pi/2, max(float64(shown), .02)*2*math.Pi, ink)
	return layout.Dimensions{Size: size}
}

// reset forgets the download drawn last, for the next to grow from
// nothing.
func (d *downloadRing) reset() {
	d.last, d.progress = 0, wdk.FloatTween{}
}

// arc strokes, width wide, the arc of the circle inset into a square of
// diameter from start through sweep radians, clockwise from the right.
func arc(gtx layout.Context, diameter int, inset, width float32, start, sweep float64, ink color.NRGBA) {
	center := float32(diameter) / 2
	radius := center - inset - width/2
	var path clip.Path
	path.Begin(gtx.Ops)
	steps := max(2, int(sweep*16))
	for i := 0; i <= steps; i++ {
		angle := start + sweep*float64(i)/float64(steps)
		point := f32.Pt(center+radius*float32(math.Cos(angle)), center+radius*float32(math.Sin(angle)))
		if i == 0 {
			path.MoveTo(point)
		} else {
			path.LineTo(point)
		}
	}
	if sweep >= 2*math.Pi {
		path.Close()
	}
	paint.FillShape(gtx.Ops, ink, clip.Stroke{Path: path.End(), Width: width}.Op())
}
