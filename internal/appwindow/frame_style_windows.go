// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows

package appwindow

import (
	"image/color"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	dwmwaUseImmersiveDarkModeBefore20H1 = 19
	dwmwaUseImmersiveDarkMode           = 20
	dwmwaCaptionColor                   = 35
)

var dwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

// applyFrameStyle styles the Windows 10/11 system title bar to match the
// application's dark/light theme and surface color.
func applyFrameStyle(hwnd uintptr, darkSet, dark bool, c color.NRGBA) {
	if hwnd == 0 || dwmSetWindowAttribute.Find() != nil {
		return
	}
	if darkSet {
		var val int32
		if dark {
			val = 1
		}
		if hr, _, _ := dwmSetWindowAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&val)), unsafe.Sizeof(val)); hr != 0 {
			dwmSetWindowAttribute.Call(hwnd, dwmwaUseImmersiveDarkModeBefore20H1, uintptr(unsafe.Pointer(&val)), unsafe.Sizeof(val))
		}
	}
	if c.A != 0 {
		// Win32 COLORREF is 0x00BBGGRR.
		cref := uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16
		dwmSetWindowAttribute.Call(hwnd, dwmwaCaptionColor, uintptr(unsafe.Pointer(&cref)), unsafe.Sizeof(cref))
	}
}
