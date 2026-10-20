// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gioui.org/io/key"
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

// Send is an icon at the capsule's end, where the microphone is while
// nothing is written, as in Telegram Desktop, and sends what is written.
func TestComposerSendsByItsIcon(t *testing.T) {
	h := newComposerHarness(t)
	h.frame()
	h.router.Queue(key.EditEvent{Text: "по иконке"})
	h.frame()
	// The far slot of the capsule: 16dp in, a 56dp square, a pixel a dp.
	h.click(float32(h.size.X-16-28), float32(h.size.Y-12-28))
	deadline := time.Now().Add(time.Second)
	for h.p.composer.draft(1).sending && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		h.frame()
	}
	if d := h.p.composer.draft(1); d.err != nil || d.editor.Text() != "" {
		t.Fatalf("the icon did not send: %v / %q", d.err, d.editor.Text())
	}
	hist := h.p.source.History(1)
	if got := hist.Messages[len(hist.Messages)-1].Text; got != "по иконке" {
		t.Fatalf("sent %q", got)
	}
}
