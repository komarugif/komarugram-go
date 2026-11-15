// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"gioui.org/app"
	"gioui.org/io/event"

	"komarugram/internal/wallpaper"
)

// desktopView is the native window an event tells of, the zero View when
// it is gone.
func desktopView(e event.Event) (wallpaper.View, bool) {
	v, ok := e.(app.Win32ViewEvent)
	return wallpaper.View{Window: v.HWND}, ok
}

var dwmIsCompositionEnabled = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmIsCompositionEnabled")

// composes reports whether the desktop is composed: always from Windows 8;
// on Windows 7 not with the Basic and Classic themes, where a window is
// opaque whatever it draws, and only its own blur is there.
func composes() bool {
	var on int32
	if hr, _, _ := dwmIsCompositionEnabled.Call(uintptr(unsafe.Pointer(&on))); hr != 0 {
		return false
	}
	return on != 0
}
