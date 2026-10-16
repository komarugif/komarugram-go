// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"image"
	"strings"
	"testing"

	"github.com/srwiley/oksvg"
	"rsc.io/qr"

	"gioui.org/unit"

	"komarugram/assets"
)

// TestQRLogoIsDrawnAsItLooks checks the logo of the QR code as oksvg draws
// logo.svg at that size: its background is the gradient, not the black of
// a gradient oksvg did not read, and its strokes are as thin as at full
// size, not wide enough to cover the cat.
func TestQRLogoIsDrawnAsItLooks(t *testing.T) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(assets.Logo))
	if err != nil {
		t.Fatal(err)
	}
	for _, px := range []int{44, 55, 88} {
		img := rasterizeLogo(icon, px)
		// The top left corner is the gradient's first color, #294677.
		c := img.RGBAAt(1, 1)
		if c.B < 90 || c.B < c.R+40 {
			t.Errorf("%d px: corner %v, want the blue of the gradient", px, c)
		}
		orange := 0
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] > 200 && img.Pix[i+1] > 120 && img.Pix[i+2] < 120 {
				orange++
			}
		}
		// The cat's strokes are about a fifth of the logo.
		if share := float64(orange) / float64(px*px); share < 0.08 || share > 0.35 {
			t.Errorf("%d px: %.0f%% of the logo is the strokes' orange", px, 100*share)
		}
	}
}

// TestQRHoleLeavesTheCodeReadable checks that the logo takes out of the
// code few enough modules for its correction, Quartile, which restores a
// quarter of the codewords, at the densities of screens, and none of a
// finder pattern.
func TestQRHoleLeavesTheCodeReadable(t *testing.T) {
	for _, token := range []int{24, 44} {
		code, err := qr.Encode("tg://login?token="+strings.Repeat("A", token), qr.Q)
		if err != nil {
			t.Fatal(err)
		}
		for _, density := range []float32{1, 1.25, 1.5, 2, 3} {
			dp := func(v unit.Dp) int { return int(float32(v)*density + 0.5) }
			modulePx := max(dp(qrMaxSize)/code.Size, 1)
			hole := qrHole(code.Size, modulePx, dp(qrLogoSize)+2*dp(qrLogoGap))
			// The codewords lie in blocks of 2×4 modules: a token of
			// Telegram's, 44 characters, makes a code of 41 modules with 48
			// codewords to restore, and the 13×13 modules of the hole touch
			// about 35 of them. This keeps that margin.
			if share := float64(hole.Dx()*hole.Dy()) / float64(code.Size*code.Size); share > 0.13 {
				t.Errorf("%d modules, density %v: the logo covers %.1f%% of the modules", code.Size, density, 100*share)
			}
			finders := []image.Rectangle{
				image.Rect(0, 0, 8, 8),
				image.Rect(code.Size-8, 0, code.Size, 8),
				image.Rect(0, code.Size-8, 8, code.Size),
			}
			for _, f := range finders {
				if hole.Overlaps(f) {
					t.Errorf("%d modules, density %v: the logo %v covers the finder %v", code.Size, density, hole, f)
				}
			}
			if hole.Min.X+hole.Max.X != code.Size || hole.Min.Y != hole.Min.X || hole.Dx() != hole.Dy() {
				t.Errorf("%d modules, density %v: hole %v is not a square in the middle", code.Size, density, hole)
			}
		}
	}
}
