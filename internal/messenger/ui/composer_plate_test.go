// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
)

// brightness is the sum of a pixel's channels.
func brightness(im *image.RGBA, x, y int) int {
	c := im.RGBAAt(x, y)
	return int(c.R) + int(c.G) + int(c.B)
}

// The floating composer's capsule, round end to end, casts a shadow on the
// history under it, as menus and dialogs do; the classic bar casts none.
func TestFloatingComposerCastsAShadow(t *testing.T) {
	for _, classic := range []bool{false, true} {
		h := newComposerHarness(t)
		h.p.classic = func() bool { return classic }
		h.chat = 2
		h.frame()
		h.frame()
		top := h.p.composer.top
		im := h.screenshot(t)
		// 3px below the capsule, in the 12dp gap above the window's edge
		// (a pixel is a dp here), and as far down at the window's side, out
		// of the shadow's reach.
		x, y, farX, farY := h.size.X/2, h.size.Y-9, 1, h.size.Y-9
		if classic {
			// At the window's side, just above the bar and what is over it,
			// and higher up.
			x, y, farX, farY = 1, top-3, 1, top-60
		}
		near, far := brightness(im, x, y), brightness(im, farX, farY)
		switch {
		case !classic && near+15 > far:
			t.Errorf("under the floating capsule the history is %d bright, %d away from it: no shadow", near, far)
		case classic && near != far:
			t.Errorf("above the classic bar the history is %d bright, %d away from it", near, far)
		}
	}
}
