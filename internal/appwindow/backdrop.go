// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"image"
	"image/color"
	"runtime"
	"sync"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"komarugram/internal/crash"
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
	// the look, and shownGlass the glass they were made of.
	shown      *wallpaper.Desktop
	shownGlass wallpaper.Glass
	look       desktopLook
	images     map[pictureKey]backdropImage
	// Aero's glass is made anew, in the background, when its streaks move
	// with the window: made gets what is ready, making is what is being
	// made, and generation tells what was made for images as they are.
	made       *glassMade
	making     map[pictureKey]bool
	generation int
	// glass is which glass of Windows 7 the window has: of a window with
	// the system's frame, active or not, or of one without it.
	glass wallpaper.GlassState
	// color is the color of the windows the settings chose, 0xAARRGGBB, for
	// Windows 7 without composition (WindowColorOffered); 0 for the
	// system's.
	color uint32
	// asked is whether the content asked for the desktop, in the frame
	// before or in this one, and drawn whether it is drawn in this one.
	asked, drawn bool
}

// pictureKey is a screen's picture, for the glass of a state: a window
// keeps both of its states, not to make one anew whenever it is activated.
type pictureKey struct {
	picture *image.RGBA
	glass   wallpaper.GlassState
}

// backdropImage is a screen's picture as it is drawn: for the glass, with
// its streaks moved by shift pixels of the picture.
type backdropImage struct {
	op    paint.ImageOp
	shift int
}

// glassMade is the glass made in the background, for the window's goroutine
// to take.
type glassMade struct {
	mu   sync.Mutex
	done []glassPicture
}

// glassPicture is the glass made of a screen's picture for a state, nil
// where making it failed.
type glassPicture struct {
	generation int
	key        pictureKey
	shift      int
	img        *image.RGBA
}

func (m *glassMade) put(p glassPicture) {
	m.mu.Lock()
	m.done = append(m.done, p)
	m.mu.Unlock()
}

func (m *glassMade) take() []glassPicture {
	m.mu.Lock()
	defer m.mu.Unlock()
	done := m.done
	m.done = nil
	return done
}

// forget drops the pictures made, and what is being made, for another
// desktop or look.
func (b *backdrop) forget() {
	b.images = nil
	b.making = nil
	b.generation++
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
	b := w.backdrop
	w.backdrop = backdrop{view: view, on: b.on, glass: b.glass, color: b.color}
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
	case "windows":
		// Not with the Basic and Classic themes of Windows 7, under which
		// the desktop is not composed.
		return composes()
	case "darwin":
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
		b.shown = nil
		b.forget()
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
	glass := w.desktopGlass(desktop)
	if glass != nil && *glass != b.shownGlass {
		// Another color chosen, or composition turned on or off.
		b.images = nil
	}
	if b.shown != desktop || b.images == nil {
		b.shown = desktop
		if glass != nil {
			b.shownGlass = *glass
		}
		b.forget()
		b.images = map[pictureKey]backdropImage{}
	}
	if b.made != nil {
		for _, p := range b.made.take() {
			if p.generation != b.generation {
				continue
			}
			delete(b.making, p.key)
			if p.img != nil {
				b.images[p.key] = backdropImage{op: imageOp(p.img), shift: p.shift}
			}
		}
	}
	window := image.Rectangle{Max: size}
	for _, s := range desktop.Screens {
		at := s.Bounds.Sub(origin)
		visible := at.Intersect(window)
		if s.Picture == nil || visible.Empty() {
			continue
		}
		key := pictureKey{picture: s.Picture}
		if glass != nil {
			key.glass = b.glass
		}
		img, ok := b.images[key]
		switch {
		case glass != nil:
			// The streaks move with the window, by an eighth of how far
			// its center is from the screen's.
			shift := 0
			if s.Reflection != nil {
				shift = wallpaper.ReflectionShift(s.Bounds, s.Picture.Bounds().Size(), origin.X+size.X/2)
			}
			if !ok {
				// The first picture is made at once, not to show the
				// surfaces opaque meanwhile.
				img = backdropImage{op: imageOp(wallpaper.GlassUnder(nil, s, *glass, key.glass, shift, b.look.fill, float64(b.look.show))), shift: shift}
				b.images[key] = img
			} else if img.shift != shift {
				w.remakeGlass(s, key, *glass, shift)
			}
		case !ok:
			img = backdropImage{op: imageOp(wallpaper.Acrylic(s.Picture, b.look.fill, float64(b.look.show)))}
			b.images[key] = img
		}
		picture := s.Picture.Bounds().Size()
		area := clip.Rect(visible).Push(gtx.Ops)
		scale := f32.Pt(float32(at.Dx())/float32(picture.X), float32(at.Dy())/float32(picture.Y))
		move := f32.Pt(float32(at.Min.X), float32(at.Min.Y))
		stretch := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, scale).Offset(move)).Push(gtx.Ops)
		img.op.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		stretch.Pop()
		area.Pop()
		b.drawn = true
	}
}

// WindowColorOffered reports whether the settings offer the color of the
// windows: on Windows 7 while the desktop is not composed, under the Basic
// and Classic themes, where Windows offers none and the glass keeps the
// color the theme wrote.
func WindowColorOffered() bool {
	return wallpaper.AeroGlass() && !composes()
}

// SetWindowColor gives the color of the windows the settings chose,
// 0xAARRGGBB, its alpha the intensity, or 0 for the system's. It colors the
// glass the window draws while WindowColorOffered.
func (w *Window) SetWindowColor(argb uint32) {
	if w.backdrop.color != argb {
		w.backdrop.color = argb
		w.Invalidate()
	}
}

// desktopGlass is the glass the window draws over the desktop: the
// desktop's, of the color the settings chose where WindowColorOffered; nil
// for acrylic.
func (w *Window) desktopGlass(d *wallpaper.Desktop) *wallpaper.Glass {
	if d.Glass == nil {
		return nil
	}
	if c := w.backdrop.color; c != 0 && WindowColorOffered() {
		g := wallpaper.GlassOfColor(c)
		return &g
	}
	return d.Glass
}

// imageOp is a picture of the desktop, to be drawn stretched.
func imageOp(img *image.RGBA) paint.ImageOp {
	op := paint.NewImageOp(img)
	op.Filter = paint.FilterLinear
	return op
}

// remakeGlass makes the glass of the screen anew, with its streaks moved by
// shift pixels, in the background: the window draws the picture it has
// until then. One picture of a screen is made at a time; when it is done,
// the window takes it in its next frame and asks for another if the
// streaks have moved since.
func (w *Window) remakeGlass(s wallpaper.Screen, key pictureKey, glass wallpaper.Glass, shift int) {
	b := &w.backdrop
	if b.making[key] {
		return
	}
	if b.making == nil {
		b.making = map[pictureKey]bool{}
	}
	if b.made == nil {
		b.made = new(glassMade)
	}
	b.making[key] = true
	made, look, generation := b.made, b.look, b.generation
	go func() {
		p := glassPicture{generation: generation, key: key, shift: shift}
		defer func() {
			made.put(p)
			w.Invalidate()
		}()
		defer crash.Recover("desktop glass", func(*crash.Panic) {})
		p.img = wallpaper.GlassUnder(nil, s, glass, key.glass, shift, look.fill, float64(look.show))
	}()
}

// glassState is which glass of Windows 7 the DWM would draw behind the
// window as it is configured: another while the window is active. Gio's
// window keeps the style of one with the system's frame when it draws its
// own (it takes the frame off its client area only), and the DWM gives it
// that glass; only a full-screen window is a popup to it.
func glassState(c app.Config) wallpaper.GlassState {
	switch {
	case c.Mode == app.Fullscreen:
		return wallpaper.GlassFrameless
	case c.Focused:
		return wallpaper.GlassActive
	}
	return wallpaper.GlassInactive
}
