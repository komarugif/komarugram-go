// SPDX-License-Identifier: Unlicense OR MIT

package ratex

// List is RaTeX's display list (docs/DISPLAYLIST_JSON_PROTOCOL.md in
// RaTeX): what a formula draws, in em, from the top left of its box, its
// baseline Height under the top.
type List struct {
	Width  float32 `json:"width"`
	Height float32 `json:"height"`
	Depth  float32 `json:"depth"`
	Items  []Item  `json:"items"`
}

// Item kinds, Item.Type.
const (
	GlyphPath = "GlyphPath"
	Line      = "Line"
	Rect      = "Rect"
	Path      = "Path"
)

// Item is one thing a formula draws: a glyph of a KaTeX font at its
// baseline's origin X, Y, Scale em high; a line centred on Y; a
// rectangle; or a path whose commands are relative to X, Y. A kind this
// package does not know is not drawn.
type Item struct {
	Type      string    `json:"type"`
	X         float32   `json:"x"`
	Y         float32   `json:"y"`
	Scale     float32   `json:"scale"`
	Font      string    `json:"font"`
	CharCode  rune      `json:"char_code"`
	Width     float32   `json:"width"`
	Height    float32   `json:"height"`
	Thickness float32   `json:"thickness"`
	Dashed    bool      `json:"dashed"`
	Fill      bool      `json:"fill"`
	Commands  []Command `json:"commands"`
	Color     *Color    `json:"color"`
}

// Command kinds, Command.Type.
const (
	MoveTo  = "MoveTo"
	LineTo  = "LineTo"
	CubicTo = "CubicTo"
	QuadTo  = "QuadTo"
	Close   = "Close"
)

// Command is one step of a path, in em from the path's X, Y.
type Command struct {
	Type string  `json:"type"`
	X    float32 `json:"x"`
	Y    float32 `json:"y"`
	X1   float32 `json:"x1"`
	Y1   float32 `json:"y1"`
	X2   float32 `json:"x2"`
	Y2   float32 `json:"y2"`
}

// Color is an item's colour, each part from 0 to 1. The module lays a
// formula out in transparent black, so an item of that colour has none of
// its own: \color and \textcolor give one.
type Color struct {
	R float32 `json:"r"`
	G float32 `json:"g"`
	B float32 `json:"b"`
	A float32 `json:"a"`
}

// Own reports whether the colour is one the formula set, not its text's.
func (c *Color) Own() bool { return c != nil && c.A > 0 }
