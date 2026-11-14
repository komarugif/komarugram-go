// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package appwindow

import (
	"gioui.org/app"
	"gioui.org/io/event"

	"komarugram/internal/wallpaper"
)

// desktopView is the native window an event tells of, the zero View when
// it is gone.
func desktopView(e event.Event) (wallpaper.View, bool) {
	v, ok := e.(app.HaikuViewEvent)
	return wallpaper.View{Window: uintptr(v.Window)}, ok
}
