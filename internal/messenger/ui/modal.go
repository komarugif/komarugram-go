// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// Open and close animations of dialogs, as the overlay of gio-mw has them:
// a dialog grows and fades in, and shrinks and fades out faster.
const (
	modalEnterDuration = token.DurationMedium2
	modalExitDuration  = token.DurationShort4
	modalEnterScale    = 0.9
)

// modal frames a modal dialog: a scrim over the area, which takes all the
// input under the dialog, and the dialog in the middle of it. Escape and a
// click on the scrim close it, and the dialog animates in and out.
//
// A dialog's owner keeps what the dialog shows until Layout reports that
// the dialog closed, so that it can animate out.
type modal struct {
	height         heightTransition
	shown, closing bool
	// focus takes the keyboard focus on the next frame, from whatever is
	// under the dialog.
	focus      bool
	visibility wdk.FloatTween
	scrim      widget.Clickable
	// panel takes the clicks on the dialog itself, which would otherwise
	// reach the scrim.
	panel struct{}
	// back, if set, handles Escape first: it reports whether it went back
	// within the dialog, which then stays open.
	back func() bool
	// toast tells the outcome of what was done in the dialog, at its
	// bottom.
	toast toast
}

// Open shows the dialog, animating in.
func (m *modal) Open() {
	*m = modal{shown: true, focus: true, back: m.back}
}

// Close animates the dialog out; Layout reports when it is done.
func (m *modal) Close() {
	if m.shown {
		m.closing = true
	}
}

// Toast shows text at the bottom of the dialog for a few seconds: an error,
// or the outcome of an action, which the dialog does not keep as text.
func (m *modal) Toast(text string) { m.toast.Show(text) }

// Hide removes the dialog at once, as when what it is about goes away.
func (m *modal) Hide() {
	m.shown, m.closing = false, false
}

// Shown reports whether the dialog is shown, animating out included.
func (m *modal) Shown() bool {
	return m.shown
}

// Update closes the dialog on Escape or a click on the scrim, unless it is
// locked, as while it waits for an operation. Layout calls it; a dialog
// shown over another is updated first, by the owner of both, to take the
// Escape before the one below.
func (m *modal) Update(gtx layout.Context, locked bool) {
	if !m.shown || m.closing {
		return
	}
	clicked := m.scrim.Clicked(gtx)
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press && !locked {
			if m.back == nil || !m.back() {
				m.Close()
			}
		}
	}
	if clicked && !locked {
		m.Close()
	}
}

// Layout draws the dialog, content, in the middle of the area. It reports
// whether the dialog is still shown: false once it closed and animated out.
func (m *modal) Layout(gtx layout.Context, locked bool, content layout.Widget) bool {
	m.Update(gtx, locked)
	if !m.shown {
		return false
	}
	visibility := m.animate(gtx)
	if m.closing && visibility == 0 {
		m.Hide()
		return false
	}
	if m.closing {
		// A closing dialog takes no input.
		gtx = gtx.Disabled()
	}
	size := gtx.Constraints.Max
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	scrim := scheme(gtx).Scrim.SetOpacity(token.OpacityLevel8)
	scrim.A = uint8(float32(scrim.A)*visibility + .5)
	paint.Fill(gtx.Ops, scrim.AsNRGBA())
	// The keyboard focus target lies under the scrim, which takes the clicks.
	event.Op(gtx.Ops, m)
	m.scrim.Layout(gtx, func(layout.Context) layout.Dimensions { return layout.Dimensions{Size: size} })
	if m.focus {
		gtx.Execute(key.FocusCmd{Tag: m})
		m.focus = false
	}

	macro := op.Record(gtx.Ops)
	inner := gtx
	inner.Constraints = layout.Constraints{Max: size}
	dims := content(inner)
	call := macro.Stop()
	defer op.Offset(size.Sub(dims.Size).Div(2)).Push(gtx.Ops).Pop()
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.panel)
	area.Pop()
	if visibility == 1 {
		call.Add(gtx.Ops)
		m.layoutToast(gtx, size, dims.Size)
		return true
	}
	scale := modalEnterScale + (1-modalEnterScale)*visibility
	center := layout.FPt(dims.Size).Mul(.5)
	defer op.Affine(f32.AffineId().Scale(center, f32.Pt(scale, scale))).Push(gtx.Ops).Pop()
	defer paint.PushOpacity(gtx.Ops, visibility).Pop()
	call.Add(gtx.Ops)
	m.layoutToast(gtx, size, dims.Size)
	return true
}

// layoutToast draws the toast under the dialog, which lies in the middle
// of area, so that it hides none of the dialog's buttons; over the
// dialog's bottom when there is no room under it.
func (m *modal) layoutToast(gtx layout.Context, area, dialog image.Point) {
	below := (area.Y - dialog.Y) / 2
	m.toast.LayoutBelow(gtx, image.Rectangle{Max: dialog}, image.Rect(0, dialog.Y, dialog.X, dialog.Y+below))
}

// animate returns how much the dialog shows, from 0 to 1.
func (m *modal) animate(gtx layout.Context) float32 {
	if m.visibility.Duration == 0 {
		// Start hidden, so that the dialog animates in on its first frame.
		m.visibility.Animate(gtx, 0)
	}
	target := float32(1)
	m.visibility.Duration = modalEnterDuration
	m.visibility.Easing = &token.EasingEmphasizedDecelerate
	if m.closing {
		target = 0
		m.visibility.Duration = modalExitDuration
		m.visibility.Easing = &token.EasingEmphasizedAccelerate
	}
	return m.visibility.Animate(gtx, target)
}

// Card animates content-driven changes independently of the dialog's entrance.
func (m *modal) Card(gtx layout.Context, content layout.Widget, padding unit.Dp) layout.Dimensions {
	return m.height.Card(gtx, content, padding)
}
