package ui

import (
	"image"
	"testing"

	"gioui.org/widget"
)

// A picture of about its tile's proportions fills it, as a thumbnail whose
// few pixels round them does: laid whole in the middle, it left strips of
// the plate at the sides. One of other proportions lies whole in the
// middle; a blurred preview, or a picture to crop, fills it.
func TestTileFit(t *testing.T) {
	tile := image.Pt(400, 400)
	for _, c := range []struct {
		name string
		pic  image.Point
		crop bool
		want widget.Fit
	}{
		{"a thumbnail of rounded proportions", image.Pt(90, 86), false, widget.Cover},
		{"of the tile's", image.Pt(320, 320), false, widget.Cover},
		{"wide", image.Pt(160, 90), false, widget.Contain},
		{"tall", image.Pt(90, 160), false, widget.Contain},
		{"wide, to crop", image.Pt(160, 90), true, widget.Cover},
	} {
		if got := tileFit(c.pic, tile, c.crop); got != c.want {
			t.Errorf("%s: %v in %v is drawn %v, want %v", c.name, c.pic, tile, got, c.want)
		}
	}
}
