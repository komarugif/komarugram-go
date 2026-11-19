// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/key"
	"gioui.org/layout"
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

func (v *mediaViewer) blocksInput() bool {
	return v.open && !v.closing.requested && !v.opening.returning()
}

func (v *mediaViewer) dismissInput(gtx layout.Context) bool {
	clicked := v.backdrop.Clicked(gtx) && !v.standalone || v.close.Clicked(gtx)
	for !clicked {
		e, ok := gtx.Event(key.FocusFilter{Target: &v.keys}, key.Filter{Focus: &v.keys, Name: key.NameEscape})
		if !ok {
			return false
		}
		if e, ok := e.(key.Event); ok {
			clicked = e.State == key.Press
		}
	}
	v.tips.hide()
	if gtx.Focused(&v.keys) {
		gtx.Execute(key.FocusCmd{})
	}
	return true
}

// Read the dismissal before registering any hit areas for the new frame.
func (v *mediaViewer) updateClosingInput(gtx layout.Context) {
	if !v.blocksInput() || !v.opening.start.IsZero() || v.edit.opened() || !v.dismissInput(gtx) {
		return
	}
	v.mu.Lock()
	waiting := len(v.items) == 0
	v.mu.Unlock()
	if waiting {
		v.Close()
		return
	}
	v.dismiss()
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
	var fade, move time.Duration
	if wdk.AnimationAllowed(gtx, wdk.AnimationViewer) {
		fade = viewerOpenFade
	}
	if wdk.AnimationAllowed(gtx, wdk.AnimationViewerAttachment) {
		move = viewerOpenMove
	}
	switch {
	case elapsed >= fade+move:
		return 0, 1, true
	case elapsed < fade:
		chrome = viewerOpenFadeAt(fade - elapsed)
	default:
		// The opening's travel run backwards.
		gone = 1 - float32(token.EasingStandard.Ease(1-float64(elapsed-fade)/float64(move)))
	}
	return chrome, gone, false
}
