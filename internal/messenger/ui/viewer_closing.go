// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
)

// viewerCloseShrink is how much smaller the picture is when it has gone.
const viewerCloseShrink = .15

// viewerClosing is the viewer being closed by the user: what opening did,
// the other way round. The controls fade out first; then the picture,
// where it is, shrinks and fades with the backdrop. It does not go back to
// the message.
type viewerClosing struct {
	requested bool
	start     time.Time
}

// dismiss closes the viewer as the user asked: with the animation, which
// the next frame starts. A viewer in a window of its own, or still opening,
// closes at once.
func (v *mediaViewer) dismiss() {
	if v.standalone || !v.open || !v.opening.start.IsZero() {
		v.Close()
		return
	}
	if !v.closing.requested {
		v.closing.requested = true
		v.invalidate()
	}
}

// step tells how far the closing is: chrome is the opacity of the controls
// and gone how much of the picture has left, 0 to 1. done is set when
// it is over, or when animations are off.
func (c *viewerClosing) step(gtx layout.Context, animate bool) (chrome, gone float32, done bool) {
	if !c.requested {
		return 1, 0, false
	}
	if !animate {
		return 0, 1, true
	}
	if c.start.IsZero() {
		c.start = gtx.Now
	}
	elapsed := gtx.Now.Sub(c.start)
	switch {
	case elapsed >= viewerOpenFade+viewerOpenMove:
		return 0, 1, true
	case elapsed < viewerOpenFade:
		chrome = viewerOpenFadeAt(viewerOpenFade - elapsed)
	default:
		// The opening's travel run backwards.
		gone = 1 - float32(token.EasingStandard.Ease(1-float64(elapsed-viewerOpenFade)/float64(viewerOpenMove)))
	}
	gtx.Execute(op.InvalidateCmd{})
	return chrome, gone, false
}
