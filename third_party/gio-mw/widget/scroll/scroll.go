// SPDX-License-Identifier: Unlicense OR MIT

// Package scroll provides a list with smooth wheel scrolling and an overlay
// scrollbar, in the manner of Chromium and Telegram Desktop. Horizontal lists
// scroll with the vertical wheel too, as tab strips do.
package scroll

import (
	"image"
	"math"
	"os"
	"runtime"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

const (
	// A wheel notch scrolls by wheelStep pixels over wheelDuration.
	wheelStep     = 100
	wheelDuration = 200 * time.Millisecond
	// Where the platform does not tell a wheel from a touchpad
	// (pointer.Event.Wheel), scroll events smaller than this are taken
	// for a touchpad's.
	preciseBelow = 40

	// pageFraction of the viewport is scrolled by a click on the track. The
	// list jumps; only the thumb glides to its new place over pageDuration.
	pageFraction = 0.875
	pageDuration = 150 * time.Millisecond

	// The scrollbar shows while the pointer is over the list, and for
	// hideDelay after the list scrolled.
	hideDelay = time.Second

	trackWidth     = unit.Dp(12)
	thumbWidth     = unit.Dp(4)
	thumbWideWidth = unit.Dp(8)
	thumbInset     = unit.Dp(2)
	thumbMinLength = unit.Dp(32)
)

// WheelScale converts scroll event distances to pixels. Gio reports a wheel
// notch as 100 pixels on Wayland, but on X11 as two events of 10 pixels (for
// the press and the release of the wheel button), so X11 is scaled up.
var WheelScale = defaultWheelScale()

func defaultWheelScale() float32 {
	if unixDesktop() && os.Getenv("WAYLAND_DISPLAY") == "" {
		return 5
	}
	return 1
}

// TouchpadScale converts the distances of a touchpad, and of the kinetic
// scrolling after it, to pixels, after WheelScale. Gio passes on the axis
// values of Wayland as they are, libinput's units, which scroll several
// times slower than other programs: Chromium multiplies them by 10, or by
// 2.5 with its WaylandUnscaledTouchpadScrolling; lists here by 2.5. On X11
// Gio makes 20 of a unit of XInput 2's smooth scrolling, the scrolling of a
// wheel's notch, and xf86-input-libinput makes such a unit of 15 of
// libinput's units: 2.5 pixels each there too take 0.375 of the 100 pixels
// of a notch. Other platforms send pixels.
var TouchpadScale = defaultTouchpadScale()

func defaultTouchpadScale() float32 {
	if unixDesktop() {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return 2.5
		}
		return 2.5 * 15 / wheelStep
	}
	return 1
}

// ContinuousScale is TouchpadScale for continuous scrolling
// (pointer.Event.Continuous), which Wayland tells apart: a trackpoint's, in
// the same units as a touchpad's. A trackpoint's scrolling stops when the
// stick is let go, with nothing like the kinetic scrolling that carries a
// touchpad's swipe on, and with a touchpad's scale it took too long to go
// far; it is twice as fast. The factor was chosen, not measured.
var ContinuousScale = 2 * TouchpadScale

func unixDesktop() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "freebsd" || runtime.GOOS == "openbsd"
}

// wheelKnown tells whether Gio tells a wheel's notches from a touchpad on
// this platform (pointer.Event.Wheel): it does on X11, Wayland, Windows, macOS
// and Haiku.
var wheelKnown = unixDesktop() || runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "haiku"

// NotchPixels is how far a wheel's notch scrolls a list, and what Pixels
// gives for one on X11 and on Windows; on Wayland the compositor chooses
// (150 on KDE Plasma).
const NotchPixels = wheelStep

// IsWheel tells whether scroll event e is of a wheel's notches, which glide,
// rather than of a touchpad or of kinetic scrolling, which follow the
// fingers at once.
func IsWheel(e pointer.Event) bool {
	if wheelKnown {
		return e.Wheel
	}
	return math.Abs(float64(e.Scroll.X*WheelScale)) >= preciseBelow || math.Abs(float64(e.Scroll.Y*WheelScale)) >= preciseBelow
}

// Pixels is how far scroll event e scrolls a list, in pixels, the same on
// every platform: about NotchPixels a wheel's notch, which X11 sends as two
// events, and as far as the fingers went on a touchpad.
func Pixels(e pointer.Event) f32.Point {
	d := e.Scroll.Mul(WheelScale)
	switch {
	case IsWheel(e):
	case e.Continuous:
		d = d.Mul(ContinuousScale)
	default:
		d = d.Mul(TouchpadScale)
	}
	return d
}

// Trace, when set, is told of every scroll event a List receives: the event
// as the platform sent it, the distance in pixels it scrolls the list by,
// and whether that distance is applied at once, as a touchpad's, or
// animated, as a wheel notch's. It is for measuring what each platform sends, and is called
// on the goroutine of the window the list is in.
var Trace func(e pointer.Event, distance float32, precise bool)

// List is a layout.List with smooth wheel scrolling and an overlay
// scrollbar: at the right edge of vertical lists, at the bottom of
// horizontal ones.
// Measurements optionally supplies exact/estimated prefix sums for variable rows.
// Find maps a pixel offset to an item and the offset inside it.
type Measurements interface {
	Total() int64
	Prefix(int) int64
	Find(int64) (int, int)
}
type List struct {
	Measurements Measurements
	layout.List
	// HideScrollbar turns the scrollbar off; smooth scrolling stays.
	HideScrollbar bool

	// Tags of the input areas.
	area, track byte

	// Wheel animation: total distance, the part applied so far and the
	// fractional pixels not applied yet.
	wheelTotal   float32
	wheelApplied float32
	wheelStart   time.Time
	remainder    float32

	hovered      bool
	trackHovered bool
	lastActive   time.Time
	thumbDrag    gesture.Drag
	dragging     bool
	// Where the drag started, in list coordinates, and where the thumb was
	// then; dragThumb is where the pointer has taken the thumb since.
	pressY     float32
	pressThumb float32
	dragThumb  float32
	// Scrollbar geometry of the previous frame, in pixels: the thumb, the
	// room it moves in and the content distance that room stands for.
	thumbStart, thumbLength int
	inset, free             int
	scrollable              float32
	elements                int
	viewport                int
	// A page jump animates the thumb from pageFrom (in track pixels).
	pageFrom  float32
	pageStart time.Time

	visibility wdk.FloatTween
	width      wdk.FloatTween
}

// Layout lays out n elements like layout.List.Layout.
func (l *List) Layout(gtx layout.Context, n int, w layout.ListElement) layout.Dimensions {
	l.update(gtx)

	before := l.Position
	moved := l.applyWheel(gtx)
	dims := l.List.Layout(gtx, n, w)
	if moved != 0 && l.Position.First == before.First && l.Position.Offset == before.Offset-moved {
		// The list is at its end in the direction of the animation.
		l.stopWheel()
	}
	l.viewport = l.main(dims.Size)
	if l.Measurements != nil {
		l.Position.Length = int(l.Measurements.Total())
	}

	// Scroll and hover events of the whole list, passed on to the
	// elements for everything else.
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &l.area)
	pass.Pop()
	area.Pop()

	if !l.HideScrollbar {
		l.layoutScrollbar(gtx, n, dims.Size)
	}
	return dims
}

// main returns the coordinate of p along the list axis.
func (l *List) main(p image.Point) int {
	if l.Axis == layout.Horizontal {
		return p.X
	}
	return p.Y
}

func (l *List) mainF(x, y float32) float32 {
	if l.Axis == layout.Horizontal {
		return x
	}
	return y
}

// rect returns the rectangle spanning [m0, m1) along the list axis and
// [c0, c1) across it.
func (l *List) rect(m0, m1, c0, c1 int) image.Rectangle {
	if l.Axis == layout.Horizontal {
		return image.Rect(m0, c0, m1, c1)
	}
	return image.Rect(c0, m0, c1, m1)
}

// ScrollBy scrolls smoothly by distance pixels, like a wheel does.
func (l *List) ScrollBy(gtx layout.Context, distance float32) {
	l.lastActive = gtx.Now
	if !wdk.AnimationsEnabled(gtx) || gtx.Now.IsZero() {
		l.stopWheel()
		l.addOffset(distance)
		return
	}
	// Continue from what is left of a running animation.
	remaining := l.wheelTotal - l.wheelApplied
	if remaining*distance < 0 {
		remaining = 0 // Reversing direction stops the previous scroll.
	}
	l.wheelTotal = remaining + distance
	l.wheelApplied = 0
	l.wheelStart = gtx.Now
}

func (l *List) stopWheel() {
	l.wheelTotal, l.wheelApplied = 0, 0
}

// stopPageGlide puts the thumb where the list is.
func (l *List) stopPageGlide() {
	l.pageStart = time.Time{}
}

// applyWheel moves the list by the part of the wheel animation due in this
// frame and returns the whole pixels it moved the list by.
func (l *List) applyWheel(gtx layout.Context) int {
	if l.wheelTotal == 0 {
		return 0
	}
	progress := float64(gtx.Now.Sub(l.wheelStart)) / float64(wheelDuration)
	done := progress >= 1 || !wdk.AnimationsEnabled(gtx)
	target := l.wheelTotal
	if !done {
		target = l.wheelTotal * float32(token.EasingStandard.Ease(progress))
	}
	delta := target - l.wheelApplied
	l.wheelApplied = target
	moved := l.addOffset(delta)
	if done {
		l.stopWheel()
	} else {
		gtx.Execute(op.InvalidateCmd{})
	}
	return moved
}

// addOffset moves the list by whole pixels, keeps the fraction for later and
// returns the whole pixels.
func (l *List) addOffset(distance float32) int {
	l.remainder += distance
	whole := float32(math.Trunc(float64(l.remainder)))
	l.remainder -= whole
	if l.Measurements != nil {
		l.seek(int64(l.scrolled()) + int64(whole))
	} else {
		l.Position.Offset += int(whole)
	}
	l.Position.BeforeEnd = true
	return int(whole)
}

func (l *List) update(gtx layout.Context) {
	for {
		all := pointer.ScrollRange{Min: math.MinInt32, Max: math.MaxInt32}
		filter := pointer.Filter{
			Target:  &l.area,
			Kinds:   pointer.Scroll | pointer.Enter | pointer.Leave,
			ScrollY: all,
		}
		if l.Axis == layout.Horizontal {
			// The vertical wheel scrolls horizontal lists as well.
			filter.ScrollX = all
		}
		ev, ok := gtx.Event(filter)
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Enter:
			l.hovered = true
		case pointer.Leave:
			l.hovered = false
		case pointer.Scroll:
			l.stopPageGlide()
			d := Pixels(e)
			distance := d.Y
			if l.Axis == layout.Horizontal && d.X != 0 {
				distance = d.X
			}
			// A touchpad scrolls along with the fingers, at once; a wheel's
			// notch glides.
			precise := !IsWheel(e)
			if Trace != nil {
				Trace(e, distance, precise)
			}
			if precise {
				l.stopWheel()
				l.addOffset(distance)
				l.lastActive = gtx.Now
			} else {
				l.ScrollBy(gtx, distance)
			}
		}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &l.track,
			Kinds:  pointer.Press | pointer.Enter | pointer.Leave,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Enter:
			l.trackHovered = true
		case pointer.Leave:
			l.trackHovered = false
		case pointer.Press:
			// A click on the track beside the thumb pages towards it.
			y := int(l.mainF(e.Position.X, e.Position.Y))
			page := float32(l.viewport) * pageFraction
			if y < l.thumbStart {
				l.pageJump(gtx, -page)
			} else if y > l.thumbStart+l.thumbLength {
				l.pageJump(gtx, page)
			}
		}
	}
	// The thumb area is only clipped, not offset, so drag positions are in
	// list coordinates. The thumb moves by exactly as much as the pointer.
	var dragTo *float32
	for {
		axis := gesture.Vertical
		if l.Axis == layout.Horizontal {
			axis = gesture.Horizontal
		}
		e, ok := l.thumbDrag.Update(gtx.Metric, gtx.Source, axis)
		if !ok {
			break
		}
		y := l.mainF(e.Position.X, e.Position.Y)
		switch e.Kind {
		case pointer.Press:
			l.dragging = true
			l.pressY = y
			l.pressThumb = float32(l.thumbStart - l.inset)
			l.dragThumb = l.pressThumb
			l.stopWheel()
		case pointer.Drag:
			dragTo = &y
		case pointer.Release, pointer.Cancel:
			l.dragging = false
		}
	}
	if dragTo != nil && l.free > 0 {
		l.dragThumb = max(0, min(float32(l.free), l.pressThumb+*dragTo-l.pressY))
		// Move the list to where that thumb position puts it.
		target := l.dragThumb / float32(l.free) * l.scrollable
		l.stopWheel()
		l.remainder = 0
		if l.Measurements != nil {
			l.seek(int64(target))
		} else {
			l.Position.Offset += int(math.Round(float64(target - l.scrolled())))
		}
		l.Position.BeforeEnd = true
		l.lastActive = gtx.Now
	}
}

// pageJump moves the list by distance at once and lets the thumb glide
// there from where it is drawn now.
func (l *List) pageJump(gtx layout.Context, distance float32) {
	l.stopWheel()
	l.pageFrom = float32(l.thumbStart - l.inset)
	l.pageStart = gtx.Now
	l.addOffset(distance)
	l.lastActive = gtx.Now
}

// scrolled estimates how far the list is scrolled, in pixels, from the
// average element length.
func (l *List) seek(pixel int64) {
	pixel = max(0, min(pixel, max(0, l.Measurements.Total()-int64(l.viewport))))
	l.Position.First, l.Position.Offset = l.Measurements.Find(pixel)
}
func (l *List) scrolled() float32 {
	if l.Measurements != nil {
		return float32(l.Measurements.Prefix(l.Position.First) + int64(l.Position.Offset))
	}
	if l.elements == 0 {
		return 0
	}
	average := float32(l.Position.Length) / float32(l.elements)
	return float32(l.Position.First)*average + float32(l.Position.Offset)
}

// layoutScrollbar draws the thumb over the right or bottom edge of the list.
func (l *List) layoutScrollbar(gtx layout.Context, n int, size image.Point) {
	length, cross := size.Y, size.X
	if l.Axis == layout.Horizontal {
		length, cross = size.X, size.Y
	}
	start, end := viewportFractions(l.Position, n, length)
	if l.Measurements != nil && l.Measurements.Total() > 0 {
		total := float32(l.Measurements.Total())
		start = clamp01(l.scrolled() / total)
		end = clamp01((l.scrolled() + float32(length)) / total)
	}
	overflows := start > 0 || end < 1

	// The track covers the list, so the pointer over the track leaves the
	// list; either keeps the scrollbar up.
	pointerOver := l.hovered || l.trackHovered || l.dragging
	visible := overflows && (pointerOver || gtx.Now.Sub(l.lastActive) < hideDelay)
	if overflows && !pointerOver && !l.lastActive.IsZero() {
		// Wake up to hide the scrollbar; re-requested every frame, as each
		// frame replaces the frames scheduled before it.
		if hideAt := l.lastActive.Add(hideDelay); gtx.Now.Before(hideAt) {
			gtx.Execute(op.InvalidateCmd{At: hideAt})
		}
	}
	target := float32(0)
	if visible {
		target = 1
	}
	l.visibility.Duration = token.DurationShort4
	visibility := l.visibility.Animate(gtx, target)
	if !overflows {
		return
	}

	wide := l.trackHovered || l.dragging
	widthTarget := float32(gtx.Dp(thumbWidth))
	if wide {
		widthTarget = float32(gtx.Dp(thumbWideWidth))
	}
	l.width.Duration = token.DurationShort3
	width := l.width.Animate(gtx, widthTarget)

	l.inset = gtx.Dp(thumbInset)
	l.elements = n
	trackLength := length - 2*l.inset
	l.thumbLength = min(max(int((end-start)*float32(trackLength)), gtx.Dp(thumbMinLength)), trackLength)
	l.free = trackLength - l.thumbLength
	l.scrollable = max(float32(l.Position.Length-length), 0)

	// The thumb stands for the scrolled distance; the ends are exact.
	var position float32
	switch {
	case l.dragging:
		position = l.dragThumb
	case !l.Position.BeforeEnd:
		position = float32(l.free)
	case l.Position.First == 0 && l.Position.Offset <= 0:
		position = 0
	case l.scrollable > 0:
		position = min(l.scrolled()/l.scrollable, 1) * float32(l.free)
	}
	if !l.pageStart.IsZero() {
		progress := float64(gtx.Now.Sub(l.pageStart)) / float64(pageDuration)
		if progress >= 1 || l.dragging || !wdk.AnimationsEnabled(gtx) {
			l.pageStart = time.Time{}
		} else {
			position = l.pageFrom + (position-l.pageFrom)*float32(token.EasingStandard.Ease(progress))
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	l.thumbStart = l.inset + int(math.Round(float64(position)))

	trackPx := gtx.Dp(trackWidth)
	trackRect := l.rect(0, length, cross-trackPx, cross)
	if visibility > 0 {
		sc := wdk.GetMaterialTheme(gtx).Scheme
		opacity := token.OpacityLevel8
		if wide {
			opacity = token.OpacityLevel9
		}
		color := sc.Surface.OnColor.SetOpacity(opacity)
		color.A = uint8(float32(color.A)*visibility + 0.5)
		w := int(width + 0.5)
		thumb := l.rect(l.thumbStart, l.thumbStart+l.thumbLength, cross-l.inset-w, cross-l.inset)
		paint.FillShape(gtx.Ops, color.AsNRGBA(), clip.UniformRRect(thumb, w/2).Op(gtx.Ops))
	}

	// The track takes input only while the scrollbar can be seen.
	if visible || l.dragging {
		// The thumb area is nested in the track area, so that the track
		// sees the pointer over the thumb as over itself.
		track := clip.Rect(trackRect).Push(gtx.Ops)
		event.Op(gtx.Ops, &l.track)
		thumb := clip.Rect(l.rect(l.thumbStart, l.thumbStart+l.thumbLength, cross-trackPx, cross)).Push(gtx.Ops)
		l.thumbDrag.Add(gtx.Ops)
		thumb.Pop()
		track.Pop()
	}
}

// viewportFractions estimates which part of the content is visible, as
// fractions of its length. It follows the estimate of Gio's material
// scrollbar.
func viewportFractions(p layout.Position, elements, viewport int) (start, end float32) {
	if elements == 0 || p.Length <= 0 {
		return 0, 1
	}
	length := float32(p.Length)
	elementLength := length / float32(elements)
	start = clamp01((float32(p.First)*elementLength + float32(p.Offset)) / length)
	end = clamp01((float32(p.First+p.Count)*elementLength + float32(p.OffsetLast)) / length)
	fraction := clamp01(end - start)
	visible := clamp01(float32(viewport) / length)
	// Spread the difference between both estimates over the two ends,
	// depending on how close the viewport is to each of them.
	if fraction < 1 {
		diff := visible - fraction
		start -= start / (1 - fraction) * diff
		end += (1 - end) / (1 - fraction) * diff
	}
	return clamp01(start), clamp01(end)
}

func clamp01(v float32) float32 {
	return max(0, min(1, v))
}
