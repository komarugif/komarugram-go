// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// Tooltips as Chromium's own (ui/views/corewm: tooltip_controller.cc,
// tooltip_aura.cc, tooltip_view_aura.cc): shown half a second after the pointer
// came over the button, for ten seconds at most, below and to the right of the
// pointer, or above it where there is no room below.
const (
	tooltipShowDelay = 500 * time.Millisecond // kDefaultShowTooltipDelay
	tooltipHideDelay = 10 * time.Second       // kDefaultHideTooltipDelay
	// tooltipOffsetX and tooltipOffsetY are where its corner is from the pointer
	// (kCursorOffsetX, kCursorOffsetY).
	tooltipOffsetX = 10
	tooltipOffsetY = 15
	// tooltipMaxWidth is the widest it is, where the window is twice as wide;
	// its text wraps (kTooltipMaxWidth).
	tooltipMaxWidth = 800
)

// tooltipColors are Chromium's kColorTooltipBackground and kColorTooltipForeground,
// its text and its border, light and dark.
var (
	tooltipLight     = [2]token.MatColor{token.NewMatColorFromHexRGB(0xffffff), token.NewMatColorFromHexRGB(0x3d3e40)}
	tooltipDarkTheme = [2]token.MatColor{token.NewMatColorFromHexRGB(0x292a2d), token.NewMatColorFromHexRGB(0xcfd1d4)}
)

// tooltips shows the tooltip of the button the pointer is over, one at a time,
// over an area: begin at the start of its frame, target in each button, and
// layout at its end, over all of it. The area follows the pointer for where to
// draw it: buttons are nested in it, and Gio tells the areas around a button of
// what goes on over it.
type tooltips struct {
	// pos is the pointer, in the area.
	pos  f32.Point
	size image.Point
	// over is the button under the pointer, since when, and its text.
	over  tooltipTag
	since time.Time
	text  string
	// shownAt is when its tooltip showed, zero before; expired tells it went for
	// good, by its time, a key or a press, until the pointer goes to another.
	shownAt time.Time
	expired bool
	// seen tells the button over was laid out in this frame: one gone takes
	// its tooltip with it.
	seen bool
}

// tooltipTag tells a button's events apart for its tooltip.
type tooltipTag struct{ c *widget.Clickable }

// begin follows the pointer over the area of gtx, which the returned stack
// holds until layout.
func (t *tooltips) begin(gtx layout.Context) clip.Stack {
	t.size = gtx.Constraints.Max
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: t, Kinds: pointer.Move | pointer.Drag | pointer.Enter | pointer.Leave | pointer.Press})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		t.pos = e.Position
		switch e.Kind {
		case pointer.Leave:
			t.leave()
		case pointer.Press:
			// Chromium's: a press hides it, and it stays hidden over that button.
			t.hide()
		}
	}
	t.seen = false
	area := clip.Rect{Max: t.size}.Push(gtx.Ops)
	event.Op(gtx.Ops, t)
	return area
}

// target makes the area of gtx, a button's, show text as its tooltip. It is
// called in the button's own area.
func (t *tooltips) target(gtx layout.Context, c *widget.Clickable, text string) {
	tag := tooltipTag{c}
	if !gtx.Enabled() {
		// Disabled, it takes no pointer, and shows nothing.
		if t.over == tag {
			t.leave()
		}
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Enter | pointer.Leave})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch {
		case e.Kind == pointer.Enter && t.over != tag:
			t.over, t.since, t.shownAt, t.expired = tag, gtx.Now, time.Time{}, false
		case e.Kind == pointer.Leave && t.over == tag:
			t.leave()
		}
	}
	event.Op(gtx.Ops, tag)
	if t.over == tag {
		t.text, t.seen = text, true
	}
}

// leave forgets the button the pointer was over.
func (t *tooltips) leave() {
	t.over, t.shownAt, t.expired = tooltipTag{}, time.Time{}, false
}

// hide hides the tooltip shown or to show, until the pointer is over another
// button: Chromium's HideAndReset, on a press or a key.
func (t *tooltips) hide() {
	if t.over != (tooltipTag{}) {
		t.expired = true
	}
}

// visible tells whether the tooltip shows now, and asks for the frame where that
// changes.
func (t *tooltips) visible(gtx layout.Context) bool {
	if !t.seen && t.over != (tooltipTag{}) {
		// The button went.
		t.leave()
	}
	if t.over == (tooltipTag{}) || t.expired || t.text == "" {
		return false
	}
	if at := t.since.Add(tooltipShowDelay); gtx.Now.Before(at) {
		gtx.Execute(op.InvalidateCmd{At: at})
		return false
	}
	if t.shownAt.IsZero() {
		t.shownAt = gtx.Now
	}
	end := t.shownAt.Add(tooltipHideDelay)
	if !gtx.Now.Before(end) {
		t.expired = true
		return false
	}
	gtx.Execute(op.InvalidateCmd{At: end})
	return true
}

// layout draws the tooltip, if one shows, over all of the area.
func (t *tooltips) layout(gtx layout.Context) {
	if !t.visible(gtx) {
		return
	}
	colors := tooltipLight
	if isDark(gtx) {
		colors = tooltipDarkTheme
	}
	// kBorderInset: the border inside it, 1 a side.
	border := gtx.Dp(1)
	padLeft, padTop, padRight, padBottom := gtx.Dp(8), gtx.Dp(4), gtx.Dp(8), gtx.Dp(5)
	maxWidth := min(gtx.Dp(tooltipMaxWidth), (t.size.X+1)/2)
	tgtx := gtx
	tgtx.Constraints = layout.Constraints{Max: image.Pt(max(1, maxWidth-padLeft-padRight), t.size.Y)}
	macro := op.Record(gtx.Ops)
	text := label(tgtx, t.text, token.TypestyleBodySmall, colors[1], 0)
	call := macro.Stop()
	size := text.Size.Add(image.Pt(padLeft+padRight, padTop+padBottom))
	at := tooltipPlace(image.Pt(int(t.pos.X), int(t.pos.Y)), size, t.size, image.Pt(gtx.Dp(tooltipOffsetX), gtx.Dp(tooltipOffsetY)))
	offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, colors[1].AsNRGBA(), clip.Rect{Max: size}.Op())
		paint.FillShape(gtx.Ops, colors[0].AsNRGBA(), clip.Rect{Min: image.Pt(border, border), Max: size.Sub(image.Pt(border, border))}.Op())
		offset(gtx, image.Pt(padLeft, padTop), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return text
		})
		return layout.Dimensions{Size: size}
	})
}

// tooltipPlace is where a tooltip of size goes for the pointer at pointer, in an
// area of bounds, as TooltipAura::GetTooltipBounds puts it: its corner off the
// pointer by off, slid back into the area, and above the pointer where it would
// go out at the bottom.
func tooltipPlace(pointer, size, bounds, off image.Point) image.Point {
	at := pointer.Add(off)
	if at.X+size.X > bounds.X {
		at.X = bounds.X - size.X
	}
	at.X = max(at.X, 0)
	if at.Y+size.Y > bounds.Y {
		at.Y = pointer.Y - size.Y
	}
	at.Y = min(max(at.Y, 0), max(bounds.Y-size.Y, 0))
	return at
}
