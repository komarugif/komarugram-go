// SPDX-License-Identifier: Unlicense OR MIT

package rectanim

import (
	"math"

	"gioui.org/f32"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// Paint draws shapes with view, which places the view box. Every
// rectangle, and every clip, is drawn as the quadrilateral its transform
// makes of it, so that no transform adds up with another.
func Paint(ops *op.Ops, shapes []Shape, view f32.Affine2D) {
	for _, s := range shapes {
		var clips []clip.Stack
		for _, c := range s.Clips {
			clips = append(clips, quad(ops, c.Box, view.Mul(c.Transform)).Push(ops))
		}
		paint.FillShape(ops, s.Color, quad(ops, s.Rect, view.Mul(s.Transform)))
		for i := len(clips) - 1; i >= 0; i-- {
			clips[i].Pop()
		}
	}
}

// quad is the outline of b placed by m. Unturned, its corners go to
// whole pixels: the pictures are pixel art, and rectangles side by side
// would otherwise show the seams their smoothed edges leave.
func quad(ops *op.Ops, b Box, m f32.Affine2D) clip.Op {
	corners := [4]f32.Point{
		m.Transform(f32.Pt(b.X, b.Y)),
		m.Transform(f32.Pt(b.X+b.W, b.Y)),
		m.Transform(f32.Pt(b.X+b.W, b.Y+b.H)),
		m.Transform(f32.Pt(b.X, b.Y+b.H)),
	}
	if _, hx, _, hy, _, _ := m.Elems(); hx == 0 && hy == 0 {
		for i := range corners {
			corners[i] = f32.Pt(float32(math.Round(float64(corners[i].X))), float32(math.Round(float64(corners[i].Y))))
		}
	}
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(corners[0])
	for _, c := range corners[1:] {
		p.LineTo(c)
	}
	p.Close()
	return clip.Outline{Path: p.End()}.Op()
}
