// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// surface is the interactive background of a clickable element: the
// background of its selected state, the state layer of hover and press, the
// press ripple and the pointer cursor. Elements embed it for its click.
type surface struct {
	click      widget.Clickable
	background wdk.ColorTween
	stateLayer wdk.ColorTween
}

// surfaceStyle is how a surface is drawn.
type surfaceStyle struct {
	// area is where the surface is drawn in the element; empty is all of it.
	area   image.Rectangle
	radius int
	// background fills the area, transparent for none. Changes animate.
	background token.MatColor
	// content is the color of what the element shows; the state layer and
	// the ripple are drawn in it.
	content token.MatColor
	// button, if set, describes the element to accessibility as a button
	// labeled so, for elements without text of their own.
	button string
}

// Clicked reports whether the element was clicked since the last call.
func (s *surface) Clicked(gtx layout.Context) bool {
	return s.click.Clicked(gtx)
}

// Layout draws the surface over an element of size, then content over it,
// and takes the element's input.
func (s *surface) Layout(gtx layout.Context, size image.Point, style surfaceStyle, content layout.Widget) layout.Dimensions {
	area := style.area
	if area.Empty() {
		area = image.Rectangle{Max: size}
	}
	shape := clip.UniformRRect(area, style.radius)
	s.stateLayer.Duration = token.DurationShort3
	layer := style.content.SetOpacity(0)
	switch {
	case s.click.Pressed() && !wdk.RipplesEnabled(gtx):
		// Without ripples, the pressed state layer shows the press.
		layer = style.content.SetOpacity(token.OpacityLevel3)
	case s.click.Hovered():
		layer = style.content.SetOpacity(token.OpacityLevel2)
	}
	for _, col := range []token.MatColor{s.background.Animate(gtx, style.background), s.stateLayer.Animate(gtx, layer)} {
		if col.A > 0 {
			paint.FillShape(gtx.Ops, col.AsNRGBA(), shape.Op(gtx.Ops))
		}
	}
	wdk.Ripple{
		Color:  style.content.SetOpacity(token.OpacityLevel3),
		Bounds: area,
		Clip:   func() clip.Op { return shape.Op(gtx.Ops) },
	}.Draw(gtx, s.click.History())
	if content != nil {
		content(gtx)
	}
	// The content is drawn outside the clickable, which clips what it
	// draws, so that a badge may reach past the element.
	return s.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if style.button != "" {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp(style.button).Add(gtx.Ops)
		}
		pointer.CursorPointer.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}
