// SPDX-License-Identifier: Unlicense OR MIT

//go:build ((linux && !android) || freebsd || openbsd) && !nox11 && cgo

package appwindow

import (
	"gioui.org/app"
	"gioui.org/io/event"

	"komarugram/internal/wallpaper"
)

// desktopView is the native window an event tells of, the zero View when
// it is gone. A window on Wayland has none to follow.
func desktopView(e event.Event) (wallpaper.View, bool) {
	v, ok := e.(app.X11ViewEvent)
	return wallpaper.View{Window: v.Window}, ok
}
