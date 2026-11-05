// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
)

// The way between the viewer and its editor. One number goes from 0 to 1 and all
// that moves is a function of it: the bar fades out first, the strip goes down
// with it, the picture moves for all the way, and the rows fade in last. Going
// back is the same number running backward, so all of it plays in reverse.
const (
	editTransitionTime = token.DurationMedium2
	// editFadeTime is the editor's picture coming over the viewer's.
	editFadeTime = token.DurationShort4
	// editLongestStep is the most time a frame counts for: the frame that
	// opens all may be slow, and it should not skip the way.
	editLongestStep = 50 * time.Millisecond
	// editRingDelay keeps a picture that loads quickly from flashing a ring.
	editRingDelay = viewerRingDelay
)

// editTransition is how far the way has gone.
type editTransition struct {
	// progress goes from 0, the viewer, to 1, the editor.
	progress float32
	// alpha is how much the editor's own picture shows, over the viewer's: it
	// comes once the way is over, and goes as the way back starts.
	alpha float32
	// tools is how much shows of what needs the picture among the rows: it
	// comes when the picture has, with the rows' own fade over it.
	tools float32
	// leaving is set on the way back; animate is what the last frame was told.
	leaving, animate bool
	last             time.Time
	started          time.Time
	// hold keeps it where it is, for a render of the way.
	hold bool
	// replaced is set when the item is what the editor made of it, of its own
	// proportions rather than those of the scene's picture.
	replaced bool
}

// stepTransition moves the way on by the time since the last frame, and reports
// whether the way back is over.
func (e *viewerEdit) stepTransition(gtx layout.Context, animate bool) (over bool) {
	t := &e.transition
	t.animate = animate
	if t.started.IsZero() {
		t.started = gtx.Now
	}
	if t.hold {
		return false
	}
	dt := time.Duration(0)
	if !t.last.IsZero() {
		dt = min(gtx.Now.Sub(t.last), editLongestStep)
	}
	t.last = gtx.Now
	target, fade := float32(1), float32(0)
	if t.leaving {
		target = 0
	} else if e.scene != nil && t.progress >= 1 {
		fade = 1
	}
	tools := float32(0)
	if e.scene != nil {
		tools = 1
	}
	if !animate {
		t.progress, t.alpha, t.tools = target, fade, tools
	} else {
		t.progress = moveTo(t.progress, target, dt, editTransitionTime)
		t.alpha = moveTo(t.alpha, fade, dt, editFadeTime)
		t.tools = moveTo(t.tools, tools, dt, editFadeTime)
	}
	if t.progress != target || t.alpha != fade || t.tools != tools || e.scene == nil && !t.leaving {
		gtx.Execute(op.InvalidateCmd{})
	}
	return t.leaving && t.progress <= 0 && t.alpha <= 0
}

// moveTo is v gone toward target by the share of whole that dt is.
func moveTo(v, target float32, dt, whole time.Duration) float32 {
	step := float32(dt) / float32(whole)
	if v < target {
		return min(target, v+step)
	}
	return max(target, v-step)
}

// leaveEdit starts the way back; closeEdit lets go of all at its end. Nothing
// animates when animations are off, nor on a frame that never told.
func (v *mediaViewer) leaveEdit() {
	e := &v.edit
	if !e.open {
		return
	}
	if !e.transition.animate {
		v.closeEdit()
		return
	}
	e.transition.leaving = true
	if e.gesture != gestureNone {
		e.scene.End()
		e.gesture = gestureNone
	}
	e.text.editing = false
	e.selected, e.armed, e.shapesOpen = -1, false, false
	e.send.dialog = editDialogNone
	e.send.modal.Hide()
	if e.stickers != nil {
		e.stickers.shown = false
	}
	if e.cancel != nil {
		// What is on its way need not come any more.
		e.cancel()
	}
}

// settled reports whether the editor takes input on its picture: the way over,
// and that picture shown.
func (e *viewerEdit) settled() bool {
	t := &e.transition
	return e.open && !t.leaving && e.scene != nil && t.progress >= 1 && t.alpha >= 1
}

// waitingLong reports whether the editor's picture is long enough on its way to
// tell it.
func (e *viewerEdit) waitingLong(gtx layout.Context) bool {
	t := &e.transition
	if t.leaving || t.started.IsZero() {
		return false
	}
	if wait := t.started.Add(editRingDelay); gtx.Now.Before(wait) {
		gtx.Execute(op.InvalidateCmd{At: wait})
		return false
	}
	return true
}

// phase is how far p has gone between from and to, from 0 to 1.
func phase(p, from, to float32) float32 {
	return max(0, min(1, (p-from)/(to-from)))
}

// easeInOut is the eased way of a thing that moves.
func easeInOut(p float32) float32 {
	return p * p * (3 - 2*p)
}

// chromeAlpha is how much shows of what the viewer has around the picture: all
// of it without an editor.
func (e *viewerEdit) chromeAlpha() float32 {
	if !e.open {
		return 1
	}
	return 1 - phase(e.transition.progress, 0, .5)
}

// stripShift is how far down the strip has gone, of its height.
func (e *viewerEdit) stripShift() float32 {
	if !e.open {
		return 0
	}
	return easeInOut(phase(e.transition.progress, 0, .7))
}

// controlsAlpha is how much shows of the editor's rows.
func (e *viewerEdit) controlsAlpha() float32 {
	if !e.open {
		return 0
	}
	return phase(e.transition.progress, .4, 1)
}

// pictureAlpha is how much shows of the viewer's picture. It stays whole under
// the editor's as that fades in, the same picture, so that the backdrop never
// shows through, and is not drawn once covered. Cut or turned it is another
// picture, and the two cross.
func (e *viewerEdit) pictureAlpha() float32 {
	alpha := e.transition.alpha
	if e.scene == nil || alpha <= 0 {
		return 1
	}
	if alpha >= 1 {
		return 0
	}
	if e.scene.Transform.Identity(e.scene.Size) {
		return 1
	}
	return 1 - alpha
}

// editFit is the scale a picture of out shows at in stage, and where.
func editFit(out image.Point, stage image.Rectangle) (float64, image.Rectangle) {
	fit := min(float64(stage.Dx())/float64(max(1, out.X)), float64(stage.Dy())/float64(max(1, out.Y)), editMaxUpscale)
	shown := image.Pt(int(float64(out.X)*fit+.5), int(float64(out.Y)*fit+.5))
	origin := stage.Min.Add(stage.Size().Sub(shown).Div(2))
	return fit, image.Rectangle{Min: origin, Max: origin.Add(shown)}
}

// pictureAt is where the viewer's picture is on the way, from where the viewer
// has it, at origin and of shown, to where the editor shows one of native pixels.
func (e *viewerEdit) pictureAt(origin, shown, native image.Point) (image.Point, image.Point) {
	if e.scene != nil && !e.transition.replaced {
		native = e.scene.Size
	}
	_, target := editFit(native, e.stage)
	p := float64(easeInOut(e.transition.progress))
	mix := func(a, b int) int { return int(math.Round(float64(a) + (float64(b)-float64(a))*p)) }
	size := target.Size()
	return image.Pt(mix(origin.X, target.Min.X), mix(origin.Y, target.Min.Y)), image.Pt(mix(shown.X, size.X), mix(shown.Y, size.Y))
}
