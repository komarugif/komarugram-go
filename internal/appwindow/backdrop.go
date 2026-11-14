// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"image"
	"image/color"
	"runtime"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"komarugram/internal/wallpaper"
)

// A window can blur the desktop behind it by itself: it draws the
// wallpaper, blurred once, under its content, where the window is over it,
// and stays opaque to the system. It looks as the compositor's blur does
// while only the wallpaper is behind the window, costs the compositor
// nothing, and is there where the compositor has no blur (X11).

// desktopSource is the wallpaper.Watcher of the window.
type desktopSource interface {
	Ready() bool
	Desktop() *wallpaper.Desktop
	Origin() (image.Point, bool)
	Event(event.Event)
	Close()
}

// backdrop is the desktop a window draws behind its content.
type backdrop struct {
	view wallpaper.View
	// source follows the window from the first time it is asked for until
	// the window's view is gone; tried is whether it was asked for.
	source desktopSource
	tried  bool
	// on is whether the content draws the desktop.
	on bool
	// shown is the desktop whose pictures images are, made for surfaces of
	// the look.
	shown  *wallpaper.Desktop
	look   desktopLook
	images map[*image.RGBA]paint.ImageOp
	// asked is whether the content asked for the desktop, in the frame
	// before or in this one, and drawn whether it is drawn in this one.
	asked, drawn bool
}

// desktopLook is what the desktop's pictures are made for: the color of
// the surfaces drawn over them, and how much of the desktop they show.
type desktopLook struct {
	fill color.NRGBA
	show float32
}

// setDesktopView tells the window's native window, the zero View when it
// is gone. It runs on the window's goroutine.
func (w *Window) setDesktopView(view wallpaper.View) {
	if w.backdrop.source != nil {
		w.backdrop.source.Close()
	}
	on := w.backdrop.on
	w.backdrop = backdrop{view: view, on: on}
}

// desktop is what follows the window over the desktop, nil where nothing
// can.
func (w *Window) desktop() desktopSource {
	b := &w.backdrop
	if !b.tried {
		b.tried = true
		// A nil *Watcher must not become an interface that is not nil.
		if watcher := wallpaper.Watch(b.view, w.Invalidate); watcher != nil {
			b.source = watcher
		}
	}
	return b.source
}

// desktopEvent gives the window's watcher an event that is its own: where
// the system tells a window of its place and of the wallpaper in events
// (Haiku), not when asked.
func (w *Window) desktopEvent(e event.Event) {
	if !wallpaper.Wants(e) {
		return
	}
	if source := w.desktop(); source != nil {
		source.Event(e)
	}
}

// OwnBlurSupported reports whether the window can draw the blurred desktop
// behind its content by itself: the system tells where the window is, and
// keeps its wallpaper where a program can read it. It is meant for the
// window's own goroutine, as the rest of the window's own blur.
func (w *Window) OwnBlurSupported() bool {
	source := w.desktop()
	return source != nil && source.Ready()
}

// CompositorBlurs reports whether the system's compositor may blur what is
// behind a transparent window; Translucency tells whether it does.
func (w *Window) CompositorBlurs() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	case "haiku":
		// app_server has no blur.
		return false
	}
	// X11 has a window to follow, and no blur; Wayland the reverse.
	return !wallpaper.Supported(w.backdrop.view)
}

// OwnBlurFromStart reports whether a window is known to be able to blur by
// itself before it is there: on Windows, where every window can. Elsewhere
// OwnBlurSupported tells, once the window is shown.
func OwnBlurFromStart() bool { return runtime.GOOS == "windows" }

// SetOwnBlur lets the content have the desktop drawn behind the window,
// with LayoutDesktop, or not.
func (w *Window) SetOwnBlur(on bool) {
	if w.backdrop.on != on {
		w.backdrop.on = on
		w.Invalidate()
	}
}

// OwnBlur reports what SetOwnBlur was told last.
func (w *Window) OwnBlur() bool { return w.backdrop.on }

// LayoutDesktop has the window draw the blurred desktop behind all of it,
// its own frame too, for the content to draw its translucent surfaces on,
// and reports whether it is drawn in this frame: not before the wallpaper
// is read, not where OwnBlurSupported is false, and not in the first frame
// it is asked for, or the first of another look, which is followed by
// another at once. It is called in the content's Layout. The surfaces
// drawn over the desktop are of the color fill and let show of it through,
// 0 to 1: the desktop is drawn so that they look as over the compositor's
// blur on Windows (wallpaper.Acrylic).
func (w *Window) LayoutDesktop(gtx layout.Context, fill color.NRGBA, show float32) bool {
	b := &w.backdrop
	if look := (desktopLook{fill: fill, show: show}); !b.asked || b.look != look {
		b.asked, b.look = true, look
		b.shown, b.images = nil, nil
		gtx.Execute(op.InvalidateCmd{})
		return false
	}
	return b.drawn
}

// DesktopDrawn reports whether the blurred desktop is behind the window in
// this frame.
func (w *Window) DesktopDrawn() bool { return w.backdrop.drawn }

// layoutBackdrop draws the blurred desktop where the window, of the size,
// is over it, before anything else of the frame, if the content asked for
// it with LayoutDesktop in the frame before.
func (w *Window) layoutBackdrop(gtx layout.Context, size image.Point) {
	b := &w.backdrop
	b.drawn = false
	if !b.on || !b.asked {
		b.asked = false
		return
	}
	source := w.desktop()
	if source == nil {
		return
	}
	desktop := source.Desktop()
	origin, ok := source.Origin()
	if desktop == nil || !ok {
		return
	}
	if b.shown != desktop || b.images == nil {
		b.shown, b.images = desktop, map[*image.RGBA]paint.ImageOp{}
	}
	window := image.Rectangle{Max: size}
	for _, s := range desktop.Screens {
		at := s.Bounds.Sub(origin)
		visible := at.Intersect(window)
		if s.Picture == nil || visible.Empty() {
			continue
		}
		img, ok := b.images[s.Picture]
		if !ok {
			img = paint.NewImageOp(wallpaper.Acrylic(s.Picture, b.look.fill, float64(b.look.show)))
			img.Filter = paint.FilterLinear
			b.images[s.Picture] = img
		}
		picture := s.Picture.Bounds().Size()
		area := clip.Rect(visible).Push(gtx.Ops)
		scale := f32.Pt(float32(at.Dx())/float32(picture.X), float32(at.Dy())/float32(picture.Y))
		move := f32.Pt(float32(at.Min.X), float32(at.Min.Y))
		stretch := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, scale).Offset(move)).Push(gtx.Ops)
		img.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		stretch.Pop()
		area.Pop()
		b.drawn = true
	}
}
