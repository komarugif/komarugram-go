// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"time"

	"gio-mw/wdk"
	"gio-mw/widget/indicator"

	"gioui.org/f32"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
)

const (
	loadingSize     unit.Dp = 48
	loadingScale            = 38.0 / 48
	loadingMorph            = 650 * time.Millisecond
	loadingRotation         = 4666 * time.Millisecond
)

// loadingIndicator uses Material 3 Expressive for animated waits and the
// classic ring when widget animations are disabled.
type loadingIndicator struct {
	started time.Time
	classic *indicator.Indicator
}

func (s *loadingIndicator) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	d := min(gtx.Dp(loadingSize), gtx.Constraints.Max.X, gtx.Constraints.Max.Y)
	if d <= 0 {
		return layout.Dimensions{}
	}
	size := image.Pt(d, d)
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	semantic.LabelOp(l.T("history.loading")).Add(gtx.Ops)
	if !wdk.AnimationsEnabled(gtx) || gtx.Now.IsZero() {
		s.started = time.Time{}
		if s.classic == nil {
			s.classic = indicator.CircularIndeterminate()
		}
		return s.classic.Layout(gtx)
	}
	if s.started.IsZero() || gtx.Now.Before(s.started) {
		s.started = gtx.Now
	}
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})

	index, blend, angle := loadingFrame(gtx.Now.Sub(s.started))
	from, to := &loadingContours[index], &loadingContours[(index+1)%len(loadingContours)]
	center := float32(d) / 2
	radius := center * loadingScale
	transform := f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(radius, radius)).
		Rotate(f32.Point{}, angle).Offset(f32.Pt(center, center))
	defer op.Affine(transform).Push(gtx.Ops).Pop()
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(from[0].Add(to[0].Sub(from[0]).Mul(blend)))
	for i := 1; i < len(from); i++ {
		path.LineTo(from[i].Add(to[i].Sub(from[i]).Mul(blend)))
	}
	path.Close()
	paint.FillShape(gtx.Ops, scheme(gtx).Primary.Color.AsNRGBA(), clip.Outline{Path: path.End()}.Op())
	return layout.Dimensions{Size: size}
}

// loadingFrame advances the shape every 650 ms with a spring and adds a
// quarter turn per transition over the continuous clockwise rotation.
func loadingFrame(elapsed time.Duration) (shape int, blend, angle float32) {
	step, within := elapsed/loadingMorph, elapsed%loadingMorph
	shape = int(step % time.Duration(len(loadingContours)))
	progress := 1.0
	if within < 500*time.Millisecond {
		// Step response of a spring with stiffness 200 and damping ratio 0.6.
		t := within.Seconds() * math.Sqrt(200)
		sin, cos := math.Sincos(0.8 * t)
		progress = 1 - math.Exp(-0.6*t)*(cos+0.75*sin)
	}
	blend = float32(min(1, max(0, progress)))
	rotation := float64(elapsed%loadingRotation) / float64(loadingRotation)
	angle = float32(2*math.Pi*rotation + (float64(step%4)+progress)*math.Pi/2)
	return shape, blend, angle
}

// sized draws the indicator in a square d wide.
func (s *loadingIndicator) sized(gtx layout.Context, l localization.Catalog, d unit.Dp) layout.Dimensions {
	n := gtx.Dp(d)
	gtx.Constraints = layout.Exact(image.Pt(n, n))
	return s.Layout(gtx, l)
}

// centered draws the indicator d wide in the middle of the constraints' width.
func (s *loadingIndicator) centered(gtx layout.Context, l localization.Catalog, d unit.Dp) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return s.sized(gtx, l, d)
	})
}

// page occupies the content viewport, keeping navigation outside it usable.
func (s *loadingIndicator) page(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	size := gtx.Constraints.Max
	fillRect(gtx, scheme(gtx).SurfaceContainerLow, size)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		n := min(gtx.Dp(loadingSize), size.X, size.Y)
		gtx.Constraints = layout.Exact(image.Pt(n, n))
		return s.Layout(gtx, l)
	})
	return layout.Dimensions{Size: size}
}
