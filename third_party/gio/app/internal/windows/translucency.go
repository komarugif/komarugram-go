// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows

package windows

import (
	"fmt"
	"sync"
	"unsafe"

	syscall "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	_DwmEnableBlurBehindWindow = dwmapi.NewProc("DwmEnableBlurBehindWindow")
	// SetWindowCompositionAttribute is not documented. It is what the
	// taskbar and the Start menu are blurred with, and what is there to
	// blur behind a window before Windows 11.
	_SetWindowCompositionAttribute = user32.NewProc("SetWindowCompositionAttribute")
	_CreateRectRgn                 = gdi32.NewProc("CreateRectRgn")
	_DeleteObject                  = gdi32.NewProc("DeleteObject")
)

// The accent states of SetWindowCompositionAttribute.
const (
	AccentDisabled = 0
	// AccentAcrylic blurs what is behind the window and tints it.
	AccentAcrylic = 4
)

const wcaAccentPolicy = 19

type accentPolicy struct {
	state, flags, color, animation uint32
}

type compositionAttribute struct {
	attribute uint32
	data      unsafe.Pointer
	size      uintptr
}

// SetWindowAccent sets the accent policy of a window: what the system draws
// behind its content. color is the tint, as 0xAABBGGRR; acrylic needs one
// that is not wholly transparent.
func SetWindowAccent(hwnd syscall.Handle, state, color uint32) error {
	if err := _SetWindowCompositionAttribute.Find(); err != nil {
		return err
	}
	policy := accentPolicy{state: state, color: color}
	data := compositionAttribute{attribute: wcaAccentPolicy, data: unsafe.Pointer(&policy), size: unsafe.Sizeof(policy)}
	r, _, err := _SetWindowCompositionAttribute.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&data)))
	if r == 0 {
		return fmt.Errorf("SetWindowCompositionAttribute: %v", err)
	}
	return nil
}

const (
	dwmBBEnable     = 1
	dwmBBBlurRegion = 2
)

type dwmBlurBehind struct {
	flags      uint32
	enable     int32
	region     syscall.Handle
	transition int32
}

// DwmBlurBehind makes the system take the alpha of what the window draws,
// or stop: with blur, Aero's glass blurs what shows through, which only
// Windows Vista and 7 draw (GlassBlur); without, DwmEnableBlurBehindWindow is
// given an empty region, which blurs nothing on any Windows.
func DwmBlurBehind(hwnd syscall.Handle, enable, blur bool) error {
	bb := dwmBlurBehind{flags: dwmBBEnable}
	switch {
	case enable && blur:
		// No region is all of the window; the region is named, as the
		// system keeps the one a call before gave.
		bb.flags, bb.enable = dwmBBEnable|dwmBBBlurRegion, 1
	case enable:
		region, _, _ := _CreateRectRgn.Call(0, 0, ^uintptr(0), ^uintptr(0))
		defer _DeleteObject.Call(region)
		bb.flags, bb.enable, bb.region = dwmBBEnable|dwmBBBlurRegion, 1, syscall.Handle(region)
	}
	r, _, _ := _DwmEnableBlurBehindWindow.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&bb)))
	if r != 0 {
		return fmt.Errorf("DwmEnableBlurBehindWindow: %#x", r)
	}
	return nil
}

var _DwmIsCompositionEnabled = dwmapi.NewProc("DwmIsCompositionEnabled")

// Composition reports whether the desktop is composed, which a window needs
// to be seen through: always from Windows 8; on Windows 7 not with the Basic
// and Classic themes, under which a window is opaque whatever it draws.
func Composition() bool {
	var on int32
	if hr, _, _ := _DwmIsCompositionEnabled.Call(uintptr(unsafe.Pointer(&on))); hr != 0 {
		return false
	}
	return on != 0
}

// GlassBlur reports whether DwmEnableBlurBehindWindow blurs, as Aero's glass
// of Windows Vista and 7 does, where there is no acrylic: behind the content
// of any window, with the system's frame or without.
var GlassBlur = sync.OnceValue(func() bool {
	v := syscall.RtlGetVersion()
	return v.MajorVersion < 6 || v.MajorVersion == 6 && v.MinorVersion < 2
})

// TransparencyEffects reports whether the user has the transparency effects
// of the system on: with them off it draws its own acrylic surfaces solid.
func TransparencyEffects() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("EnableTransparency")
	return err != nil || v != 0
}

var _DwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")

const dwmwaExtendedFrameBounds = 9

// DwmVisibleRect returns what is seen of a window: its rectangle without
// the borders the system's frame has around it for resizing, which are not
// drawn. It is the window's rectangle when the system does not say.
func DwmVisibleRect(hwnd syscall.Handle) Rect {
	var r Rect
	if hr, _, _ := _DwmGetWindowAttribute.Call(uintptr(hwnd), dwmwaExtendedFrameBounds, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r)); hr != 0 {
		return GetWindowRect(hwnd)
	}
	return r
}

var _IsWindowVisible = user32.NewProc("IsWindowVisible")

func IsWindowVisible(hwnd syscall.Handle) bool {
	r, _, _ := _IsWindowVisible.Call(uintptr(hwnd))
	return r != 0
}
