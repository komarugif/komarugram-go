// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"slices"
	"sync"

	"gioui.org/layout"
	"gioui.org/op"
)

// shapeKind is a small shape drawn the same way in many places and frames.
type shapeKind uint8

const (
	shapeVerified shapeKind = iota
	shapePremiumStar
	shapeTextBadgeFrame
)

// shapeKey names a shape as it is drawn: its kind, size and color in
// pixels, and radius, for the shapes that have one.
type shapeKey struct {
	kind   shapeKind
	size   image.Point
	color  color.NRGBA
	radius int
}

// recordedShapes is how many shapes stay recorded: the badges of names, in
// the few sizes and colors a window shows them.
const recordedShapes = 32

// shapes keeps the operations of the shapes drawn last, each recorded once.
// Gio keeps the vertices it builds of a path for as long as the operations
// that hold it stay the same: a shape drawn from them every frame is not
// built again, nor a stroke stroked again on the CPU, which made some 9 KB
// of garbage each time. Windows draw them from their own goroutines.
var shapes struct {
	mu     sync.Mutex
	recent []recordedShape // The last drawn first.
}

type recordedShape struct {
	key  shapeKey
	call op.CallOp
}

// drawShape adds the operations of the shape of key, recording them with
// draw the first time; draw must depend on nothing but what key says.
func drawShape(gtx layout.Context, key shapeKey, draw func(gtx layout.Context)) {
	shapes.mu.Lock()
	defer shapes.mu.Unlock()
	var s recordedShape
	if i := slices.IndexFunc(shapes.recent, func(s recordedShape) bool { return s.key == key }); i >= 0 {
		s = shapes.recent[i]
		shapes.recent = slices.Delete(shapes.recent, i, i+1)
	} else {
		// Operations of their own, never reset, outlive the frame: an
		// evicted shape a window still calls stays with it.
		recorded := gtx
		recorded.Ops = new(op.Ops)
		macro := op.Record(recorded.Ops)
		draw(recorded)
		s = recordedShape{key: key, call: macro.Stop()}
		if len(shapes.recent) == recordedShapes {
			shapes.recent = shapes.recent[:recordedShapes-1]
		}
	}
	shapes.recent = slices.Insert(shapes.recent, 0, s)
	s.call.Add(gtx.Ops)
}
