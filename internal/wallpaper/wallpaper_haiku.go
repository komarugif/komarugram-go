// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package wallpaper

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	_ "golang.org/x/image/bmp"

	"gioui.org/app"
	"gioui.org/io/event"

	"komarugram/internal/crash"
)

// Haiku tells a window where it is and what the desktop shows in events
// (app.HaikuPlaceEvent, app.HaikuDesktopEvent), which Gio's driver makes of
// the window's moves and of Tracker's wallpaper: the be:bgndimginfo
// attribute of the desktop's directory, a picture per workspace. The
// window gives them to its watcher, which reads the picture only once the
// desktop is asked for.

// Watcher follows a window over the desktop.
type Watcher struct {
	changed func()
	desktop atomic.Pointer[Desktop]
	closed  atomic.Bool

	mu     sync.Mutex
	origin image.Point
	placed bool
	// shows is what the desktop shows, as told last; wanted is whether the
	// desktop was asked for, and loaded what it was read from.
	shows  app.HaikuDesktopEvent
	told   bool
	wanted bool
	loaded app.HaikuDesktopEvent
	// loading is whether a goroutine reads the desktop.
	loading bool
}

// Supported reports whether a window of the view can be followed.
func Supported(v View) bool { return v.Window != 0 }

// Watch follows the window of the view, and calls changed, from another
// goroutine, when the desktop behind it is another, or the window is
// elsewhere on it. The window gives it its events with Event.
func Watch(v View, changed func()) *Watcher {
	if !Supported(v) {
		return nil
	}
	return &Watcher{changed: changed}
}

// Wants reports whether an event of the window is the watcher's.
func Wants(e event.Event) bool {
	switch e.(type) {
	case app.HaikuPlaceEvent, app.HaikuDesktopEvent:
		return true
	}
	return false
}

// Event takes an event Wants reported.
func (w *Watcher) Event(e event.Event) {
	if w == nil {
		return
	}
	switch e := e.(type) {
	case app.HaikuPlaceEvent:
		w.mu.Lock()
		moved := !w.placed || w.origin != e.Origin
		w.origin, w.placed = e.Origin, true
		w.mu.Unlock()
		if moved {
			w.tell()
		}
	case app.HaikuDesktopEvent:
		w.mu.Lock()
		w.shows, w.told = e, true
		w.mu.Unlock()
		w.load()
	}
}

// Ready reports whether the desktop can be drawn, now or once it is read.
func (w *Watcher) Ready() bool { return w != nil }

// Desktop is the desktop as it was read last, nil before that.
func (w *Watcher) Desktop() *Desktop {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	first := !w.wanted
	w.wanted = true
	w.mu.Unlock()
	if first {
		w.load()
	}
	return w.desktop.Load()
}

// Origin is where the content of the window is on the desktop.
func (w *Watcher) Origin() (image.Point, bool) {
	if w == nil {
		return image.Point{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.origin, w.placed
}

// Close stops telling of the desktop.
func (w *Watcher) Close() {
	if w != nil {
		w.closed.Store(true)
	}
}

func (w *Watcher) tell() {
	if w.changed != nil && !w.closed.Load() {
		w.changed()
	}
}

// load reads the desktop in another goroutine, if it was asked for and is
// not what was read last.
func (w *Watcher) load() {
	w.mu.Lock()
	start := w.wanted && w.told && !w.loading && w.shows != w.loaded
	if start {
		w.loading = true
	}
	w.mu.Unlock()
	if !start {
		return
	}
	go func() {
		defer crash.Recover("wallpaper", func(*crash.Panic) {
			w.mu.Lock()
			w.loading = false
			w.mu.Unlock()
		})
		for {
			w.mu.Lock()
			shows := w.shows
			if shows == w.loaded {
				w.loading = false
				w.mu.Unlock()
				return
			}
			w.mu.Unlock()
			w.desktop.Store(read(shows))
			w.mu.Lock()
			w.loaded = shows
			w.mu.Unlock()
			w.tell()
		}
	}()
}

// read makes the desktop of what it shows.
func read(shows app.HaikuDesktopEvent) *Desktop {
	if shows.Screen.X <= 0 || shows.Screen.Y <= 0 {
		return nil
	}
	var picture image.Image
	if shows.Wallpaper != "" {
		if f, err := os.Open(shows.Wallpaper); err == nil {
			// A picture that cannot be read leaves the color.
			picture, _, _ = image.Decode(f)
			f.Close()
		}
	}
	placement := Placement{Fit: At, Offset: shows.Offset}
	switch shows.Mode {
	case app.HaikuWallpaperCentered:
		placement = Placement{Fit: Center}
	case app.HaikuWallpaperScaled:
		placement = Placement{Fit: Stretch}
	case app.HaikuWallpaperTiled:
		placement = Placement{Fit: Tile}
	}
	d := &Desktop{Screens: []Screen{{
		Bounds:  image.Rectangle{Max: shows.Screen},
		Picture: Render(picture, placement, shows.Color, shows.Screen),
	}}}
	if picture != nil {
		// The picture as it was decoded is tens of megabytes.
		picture = nil
		debug.FreeOSMemory()
	}
	return d
}
