// SPDX-License-Identifier: Unlicense OR MIT
package indicator

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"image"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

const circularCycle = 1333 * time.Millisecond

// CircularIndeterminate is the classic Material spinner: a single rotating
// arc with alternating growth and shrink, without a determinate track.
func CircularIndeterminate() *Indicator { return &Indicator{kind: circularKind, indeterminate: true} }

func circularAngles(elapsed time.Duration) (start, sweep float64) {
	cycle := float64(elapsed) / float64(circularCycle)
	phase := cycle - math.Floor(cycle)
	const shortest = math.Pi / 18
	const travel = math.Pi*1.5 - shortest
	rotation := 2 * math.Pi * float64(elapsed) / float64(1400*time.Millisecond)
	start = rotation - math.Pi/2 + math.Floor(cycle)*travel
	if phase < .5 {
		sweep = shortest + travel*token.EasingStandard.Ease(phase*2)
	} else {
		tail := travel * token.EasingStandard.Ease((phase-.5)*2)
		start += tail
		sweep = shortest + travel - tail
	}
	return
}
func (i *Indicator) circularIndeterminate(gtx layout.Context) layout.Dimensions {
	d := min(gtx.Dp(diameter), min(gtx.Constraints.Max.X, gtx.Constraints.Max.Y))
	if d <= 0 {
		return layout.Dimensions{}
	}
	if i.started.IsZero() {
		i.started = gtx.Now
	}
	start, sweep := -math.Pi/2, math.Pi*1.5
	if wdk.AnimationEnabled(gtx, wdk.AnimationLoading) && !gtx.Now.IsZero() {
		start, sweep = circularAngles(max(0, gtx.Now.Sub(i.started)))
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
	}
	theme := BuildTheme(gtx)
	stroke := min(float32(gtx.Dp(theme.EnabledActiveIndicatorThickness)), float32(d)/4)
	radius := (float64(d) - float64(stroke)) / 2
	center := float64(d) / 2
	var p clip.Path
	p.Begin(gtx.Ops)
	steps := max(8, int(sweep*24))
	for n := 0; n <= steps; n++ {
		a := start + sweep*float64(n)/float64(steps)
		v := f32.Pt(float32(center+radius*math.Cos(a)), float32(center+radius*math.Sin(a)))
		if n == 0 {
			p.MoveTo(v)
		} else {
			p.LineTo(v)
		}
	}
	paint.FillShape(gtx.Ops, theme.EnabledActiveIndicatorColor.AsNRGBA(), clip.Stroke{Path: p.End(), Width: stroke}.Op())
	return layout.Dimensions{Size: image.Pt(d, d)}
}
