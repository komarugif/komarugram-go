// SPDX-License-Identifier: Unlicense OR MIT

package wdk

import (
	"image"
	"testing"
	"time"

	"gio-mw/token"

	"gioui.org/op/clip"
	"gioui.org/widget"
)

// The clip of a ripple is made only when a ripple is drawn: a widget that
// was not pressed records no shape for it.
func TestRippleClipOnlyWhenVisible(t *testing.T) {
	now := time.Now()
	shapes := 0
	r := Ripple{
		Color:  token.MatColor{A: 255},
		Bounds: image.Rect(0, 0, 100, 40),
		Clip: func() clip.Op {
			shapes++
			return clip.Rect(image.Rect(0, 0, 100, 40)).Op()
		},
	}
	r.Draw(tweenContext(now), nil)
	if shapes != 0 {
		t.Fatalf("no presses: the clip was made %d times, want 0", shapes)
	}
	presses := []widget.Press{
		{Start: now.Add(-50 * time.Millisecond)},
		{Start: now.Add(-20 * time.Millisecond)},
	}
	r.Draw(tweenContext(now), presses)
	if shapes != 1 {
		t.Fatalf("two visible ripples: the clip was made %d times, want 1", shapes)
	}
}
