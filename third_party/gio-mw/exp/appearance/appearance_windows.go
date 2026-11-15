// SPDX-License-Identifier: Unlicense OR MIT

package appearance

import (
	"image/color"
	"syscall"
	"unsafe"
)

var procRegGetValue = syscall.NewLazyDLL("advapi32.dll").NewProc("RegGetValueW")

const (
	hkeyCurrentUser = 0x80000001
	rrfRtRegDword   = 0x00000010
)

func startPlatform(m *Monitor) func() {
	return poll(m, readScheme, readAccent)
}

// readDword reads a DWORD value of the current user's registry.
func readDword(key, name string) (uint32, bool) {
	subkey, _ := syscall.UTF16PtrFromString(key)
	value, _ := syscall.UTF16PtrFromString(name)
	var data, size uint32 = 0, 4
	r, _, _ := procRegGetValue.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(subkey)), uintptr(unsafe.Pointer(value)),
		rrfRtRegDword, 0, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&size)))
	return data, r == 0
}

func readScheme() Scheme {
	data, ok := readDword(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "AppsUseLightTheme")
	if !ok {
		return Unknown
	}
	if data == 0 {
		return Dark
	}
	return Light
}

// readAccent reads the accent color: on Windows 10 and later the one
// chosen in Personalization > Colors, which DWM keeps as 0xAABBGGRR
// ("Automatically pick an accent color" writes the one picked there too);
// on Windows 8 the Start screen's; on Windows 7, which has no accent, the
// color of the windows' glass (Personalization > Window Color), which DWM
// keeps as 0xAARRGGBB, its alpha the glass's transparency.
func readAccent() (color.NRGBA, bool) {
	const dwm = `Software\Microsoft\Windows\DWM`
	if data, ok := readDword(dwm, "AccentColor"); ok {
		return abgr(data), true
	}
	if data, ok := readDword(`Software\Microsoft\Windows\CurrentVersion\Explorer\Accent`, "AccentColorMenu"); ok {
		return abgr(data), true
	}
	if data, ok := readDword(dwm, "ColorizationColor"); ok {
		return color.NRGBA{R: uint8(data >> 16), G: uint8(data >> 8), B: uint8(data), A: 255}, true
	}
	return color.NRGBA{}, false
}

func abgr(data uint32) color.NRGBA {
	return color.NRGBA{R: uint8(data), G: uint8(data >> 8), B: uint8(data >> 16), A: 255}
}
