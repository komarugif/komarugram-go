// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import (
	"image"
	"image/color"
	"math"
	"slices"
)

// Tool is a brush, as Telegram Desktop's editor has them.
type Tool uint8

const (
	Pen Tool = iota
	Arrow
	Marker
	Eraser
	Blur
)

// What brushes do to a stroke, Telegram Desktop's (editor.style).
const (
	markerOpacity    = .35
	markerWidth      = 2.5
	blurWidth        = 3.
	arrowHeadLength  = 2.5
	arrowHeadMinimum = 1.5
	arrowHeadAngle   = 26.
)

// undoLimit bounds the steps kept; each is a list of pointers, not pictures.
const undoLimit = 256

// Stroke is a line drawn with a brush.
type Stroke struct {
	Tool  Tool
	Color color.NRGBA
	// Width is the brush's, in the picture's pixels, as a pen draws it.
	Width  float64
	Points []Point
}

// lineWidth is how wide the brush draws, in the picture's pixels.
func (s *Stroke) lineWidth() float64 {
	switch s.Tool {
	case Marker:
		return s.Width * markerWidth
	case Blur:
		return s.Width * blurWidth
	}
	return s.Width
}

// bounds holds all the stroke touches.
func (s *Stroke) bounds() (lo, hi Point) {
	if len(s.Points) == 0 {
		return
	}
	lo, hi = s.Points[0], s.Points[0]
	for _, p := range s.Points {
		lo.X, lo.Y = min(lo.X, p.X), min(lo.Y, p.Y)
		hi.X, hi.Y = max(hi.X, p.X), max(hi.Y, p.Y)
	}
	margin := s.lineWidth()
	if s.Tool == Arrow {
		margin += s.Width * arrowHeadLength
	}
	return lo.Sub(Point{margin, margin}), hi.Add(Point{margin, margin})
}

// TextStyle is the plate of a text, as Telegram Desktop's editor has them.
type TextStyle uint8

const (
	TextPlain TextStyle = iota
	TextFramed
	TextSemiTransparent
	TextOpaque
)

// TextTypeface is the font of a text.
type TextTypeface uint8

const (
	TypefaceDefault TextTypeface = iota
	TypefaceItalic
	TypefaceSerif
	TypefaceCondensed
	TypefaceMonospace
)

// TextAlign is how the lines of a text stand.
type TextAlign uint8

const (
	AlignCenter TextAlign = iota
	AlignLeft
	AlignRight
)

// Text is a text on the picture.
type Text struct {
	Text     string
	Color    color.NRGBA
	Style    TextStyle
	Typeface TextTypeface
	Align    TextAlign
	// Size is the font's, in the picture's pixels.
	Size float64
	// Wrap is how wide its lines go, in sizes of its font: it keeps them as
	// the text grows.
	Wrap float64
}

// Plate is the color behind the text, none for the alpha of 0, and the color
// the text is written in: as Telegram Desktop's, by how bright its color is.
func (t *Text) Plate() (plate, ink color.NRGBA) {
	ink = t.Color
	c := float64(t.Color.R)*.299 + float64(t.Color.G)*.587 + float64(t.Color.B)*.114
	bright := c / 255
	switch t.Style {
	case TextFramed:
		plate = t.Color
		ink = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		if bright >= .721 {
			ink = color.NRGBA{A: 255}
		}
	case TextSemiTransparent:
		plate = color.NRGBA{A: 0x99}
		if bright < .25 {
			plate = color.NRGBA{R: 255, G: 255, B: 255, A: 0x99}
		}
	case TextOpaque:
		plate = color.NRGBA{A: 255}
		if bright < .25 {
			plate = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
	}
	return plate, ink
}

// ShapeType is a figure, as Telegram Desktop's editor has them.
type ShapeType uint8

const (
	ShapeCircle ShapeType = iota
	ShapeRectangle
	ShapeStar
	ShapeBubble
	ShapeArrow
)

// Shape is a figure on the picture, filling its element's box.
type Shape struct {
	Type   ShapeType
	Color  color.NRGBA
	Filled bool
	// Width is the line's, in the picture's pixels.
	Width float64
}

// Sticker is a picture on the picture. A sticker that plays is its first
// frame, here on videos too.
type Sticker struct {
	Image *image.NRGBA
}

// Element is one thing done to the picture, the older under the newer. What
// it holds never changes once it is in a scene: a change is an element made
// anew, so the steps undone are lists of the same elements.
type Element struct {
	Stroke  *Stroke
	Text    *Text
	Shape   *Shape
	Sticker *Sticker
	// Center, Width and Height place what is not a stroke, in the picture's
	// pixels, before Rotation, clockwise in degrees, and Flipped, mirroring
	// it within its box.
	Center        Point
	Width, Height float64
	Rotation      float64
	Flipped       bool
}

// Placed reports whether e has a box to pick it by.
func (e *Element) Placed() bool { return e.Stroke == nil }

// Contains reports whether p of the picture is in e's box, margin wider.
func (e *Element) Contains(p Point, margin float64) bool {
	if !e.Placed() {
		return false
	}
	q := p.Sub(e.Center).rotated(-e.Rotation)
	return math.Abs(q.X) <= e.Width/2+margin && math.Abs(q.Y) <= e.Height/2+margin
}

// Corners are the corners of e's box in the picture, clockwise from its top
// left, whose top left is the one before Flipped mirrors it.
func (e *Element) Corners() [4]Point {
	w, h := e.Width/2, e.Height/2
	out := [4]Point{{-w, -h}, {w, -h}, {w, h}, {-w, h}}
	for i, q := range out {
		out[i] = q.rotated(e.Rotation).Add(e.Center)
	}
	return out
}

// bounds holds all e touches in the picture.
func (e *Element) bounds() (lo, hi Point) {
	if e.Stroke != nil {
		return e.Stroke.bounds()
	}
	margin := 0.
	if e.Shape != nil {
		margin = e.Shape.Width
		if e.Shape.Type == ShapeArrow {
			margin += e.Height
		}
	}
	corners := e.Corners()
	lo, hi = corners[0], corners[0]
	for _, c := range corners[1:] {
		lo.X, lo.Y = min(lo.X, c.X), min(lo.Y, c.Y)
		hi.X, hi.Y = max(hi.X, c.X), max(hi.Y, c.Y)
	}
	return lo.Sub(Point{margin, margin}), hi.Add(Point{margin, margin})
}

// erases reports whether e, an eraser's stroke, may touch o, a stroke.
func (e *Element) erases(o *Element) bool {
	alo, ahi := e.bounds()
	blo, bhi := o.bounds()
	return alo.X <= bhi.X && blo.X <= ahi.X && alo.Y <= bhi.Y && blo.Y <= ahi.Y
}

// Scene is all that was done to a picture of Size. Its Elements are read by
// whoever draws them; they change through its methods, which keep what undoes
// the change.
type Scene struct {
	Size      image.Point
	Transform Transform
	Elements  []*Element
	// Trim is the part of a video kept; From is 0 and To 0 for all of it.
	Trim Trim

	undo, redo [][]*Element
	// open is the list before the step under way; opened tells one is.
	open   []*Element
	opened bool
}

// New is a scene of nothing done to a picture of size.
func New(size image.Point) *Scene {
	s := &Scene{Size: size}
	s.Transform = s.Transform.Whole(size)
	return s
}

// Begin starts a step that undoes as one, however many changes it is: a stroke
// that grows with each point, an element dragged. End closes it.
func (s *Scene) Begin() {
	if !s.opened {
		s.open, s.opened = s.Elements, true
	}
}

// End closes the step Begin started. A step that changed nothing is none.
func (s *Scene) End() {
	if !s.opened {
		return
	}
	s.opened = false
	if slices.Equal(s.open, s.Elements) {
		return
	}
	s.undo = append(s.undo, s.open)
	if len(s.undo) > undoLimit {
		s.undo = slices.Delete(s.undo, 0, len(s.undo)-undoLimit)
	}
	s.redo = nil
}

// Add puts e on top, and returns its place.
func (s *Scene) Add(e *Element) int {
	s.step(func() { s.Elements = append(slices.Clip(s.Elements), e) })
	return len(s.Elements) - 1
}

// Replace puts e in place of the element at i.
func (s *Scene) Replace(i int, e *Element) {
	if i < 0 || i >= len(s.Elements) {
		return
	}
	s.step(func() {
		s.Elements = slices.Clone(s.Elements)
		s.Elements[i] = e
	})
}

// Remove takes the element at i out.
func (s *Scene) Remove(i int) {
	if i < 0 || i >= len(s.Elements) {
		return
	}
	s.step(func() { s.Elements = slices.Delete(slices.Clone(s.Elements), i, i+1) })
}

// step makes a change, a step of its own when none is under way.
func (s *Scene) step(change func()) {
	if s.opened {
		change()
		return
	}
	s.Begin()
	change()
	s.End()
}

// CanUndo reports whether there is a step to undo.
func (s *Scene) CanUndo() bool { return !s.opened && len(s.undo) > 0 }

// CanRedo reports whether there is a step undone to do again.
func (s *Scene) CanRedo() bool { return !s.opened && len(s.redo) > 0 }

// Undo goes back a step.
func (s *Scene) Undo() {
	if s.CanUndo() {
		s.redo = append(s.redo, s.Elements)
		s.Elements = s.undo[len(s.undo)-1]
		s.undo = s.undo[:len(s.undo)-1]
	}
}

// Redo does the step undone last again.
func (s *Scene) Redo() {
	if s.CanRedo() {
		s.undo = append(s.undo, s.Elements)
		s.Elements = s.redo[len(s.redo)-1]
		s.redo = s.redo[:len(s.redo)-1]
	}
}

// Changed reports whether what comes out differs from the picture.
func (s *Scene) Changed() bool {
	if len(s.Elements) > 0 || !s.Transform.Identity(s.Size) {
		return true
	}
	return s.Trim.Set()
}

// At is the place of the topmost element at p, margin wide, -1 for none.
func (s *Scene) At(p Point, margin float64) int {
	for i, e := range slices.Backward(s.Elements) {
		if e.Contains(p, margin) {
			return i
		}
	}
	return -1
}

// BlurRadius is how far the blur of a picture of size reaches, in its pixels:
// Telegram Desktop's 20, and as much for a large picture as for one of 2000.
func BlurRadius(size image.Point) int {
	return max(20, max(size.X, size.Y)/100)
}
