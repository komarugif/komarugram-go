// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows && !haiku && !(((linux && !android) || freebsd || openbsd) && !nox11 && cgo)

package wallpaper

import (
	"image"

	"gioui.org/io/event"
)

// Watcher follows a window over the desktop. Here nothing does: Wayland
// tells a window nothing of its place, and macOS is not done.
type Watcher struct{}

// Supported reports whether a window of the view can be followed.
func Supported(View) bool { return false }

// Wants reports whether an event of the window is the watcher's.
func Wants(event.Event) bool { return false }

// Watch returns nil: the window cannot be followed.
func Watch(View, func()) *Watcher { return nil }

func (w *Watcher) Ready() bool                 { return false }
func (w *Watcher) Desktop() *Desktop           { return nil }
func (w *Watcher) Origin() (image.Point, bool) { return image.Point{}, false }
func (w *Watcher) Event(event.Event)           {}
func (w *Watcher) Close()                      {}
