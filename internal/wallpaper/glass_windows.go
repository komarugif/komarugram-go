// SPDX-License-Identifier: Unlicense OR MIT

package wallpaper

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// On Windows 7 a window that blurs by itself draws Aero's glass, colored
// as the DWM colors its own, with the streaks of the current theme
// (glass.go). The DWM tells its colorization as it is now
// (DwmGetColorizationParameters): the personalization's Window Color
// previews a color on the frames and the taskbar before it is saved, and
// the registry has it only once it is; the two differ after the key was
// written by another program too. The key is read where the DWM does not
// tell: it keeps the colorization while composition is off, under the
// Basic and Classic themes, as the last theme applied wrote it; the
// Classic theme has no streaks to read.

var (
	uxtheme             = windows.NewLazySystemDLL("uxtheme.dll")
	openThemeData       = uxtheme.NewProc("OpenThemeData")
	closeThemeData      = uxtheme.NewProc("CloseThemeData")
	getThemeRect        = uxtheme.NewProc("GetThemeRect")
	getThemeStream      = uxtheme.NewProc("GetThemeStream")
	getCurrentThemeName = uxtheme.NewProc("GetCurrentThemeName")
)

const (
	dwmKey   = `Software\Microsoft\Windows\DWM`
	themeKey = `Software\Microsoft\Windows\CurrentVersion\ThemeManager`
	// The part of the DWM's class of a theme that is the streaks, and the
	// properties of the theme's atlas of images and of a part's place in it.
	reflectionPart  = 40
	tmtDiskStream   = 213
	tmtAtlasRect    = 8002
	themeNameLength = 260
)

// aeroGlass reports whether the windows' own blur draws Aero's glass: on
// Windows 7, whose compositor draws it.
var aeroGlass = sync.OnceValue(func() bool {
	v := windows.RtlGetVersion()
	return v.MajorVersion == 6 && v.MinorVersion == 1
})

// dwmColorization is DwmGetColorizationParameters, which dwmapi.dll
// exports by its ordinal only, 0 where it does not.
var dwmColorization = sync.OnceValue(func() uintptr {
	dwmapi := windows.NewLazySystemDLL("dwmapi.dll")
	if dwmapi.Load() != nil {
		return 0
	}
	proc, err := windows.GetProcAddressByOrdinal(windows.Handle(dwmapi.Handle()), 127)
	if err != nil {
		return 0
	}
	return proc
})

// AeroGlass reports whether the windows' own blur draws Aero's glass, on
// Windows 7.
func AeroGlass() bool { return aeroGlass() }

// SystemWindowColor is the color of the windows as the system has it,
// 0xAARRGGBB, its alpha the intensity: the DWM's now, or its key's.
func SystemWindowColor() uint32 {
	if proc := dwmColorization(); proc != 0 {
		var p [7]uint32
		if hr, _, _ := syscall.SyscallN(proc, uintptr(unsafe.Pointer(&p))); hr == 0 {
			return p[0]
		}
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, dwmKey, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if v, _, err := k.GetIntegerValue("ColorizationColor"); err == nil {
			return uint32(v)
		}
	}
	return WindowColors[0]
}

// glassSettings is the colorization of the glass: as the DWM has it now,
// or as its key tells where the DWM does not; what the key lacks is
// Windows 7's own.
func glassSettings() Glass {
	if proc := dwmColorization(); proc != 0 {
		// Color, afterglow, the three balances, the reflection's
		// intensity and whether the glass is opaque, as the key has them.
		var p [7]uint32
		if hr, _, _ := syscall.SyscallN(proc, uintptr(unsafe.Pointer(&p))); hr == 0 {
			rgb := func(v uint32) color.NRGBA {
				return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
			}
			percent := func(v uint32) float64 { return float64(min(v, 100)) / 100 }
			return Glass{
				Color:            rgb(p[0]),
				Afterglow:        rgb(p[1]),
				ColorBalance:     percent(p[2]),
				AfterglowBalance: percent(p[3]),
				BlurBalance:      percent(p[4]),
				Reflection:       percent(p[5]),
				Opaque:           p[6] != 0,
			}
		}
	}
	g := SkyGlass
	k, err := registry.OpenKey(registry.CURRENT_USER, dwmKey, registry.QUERY_VALUE)
	if err != nil {
		return g
	}
	defer k.Close()
	rgb := func(name string, c *color.NRGBA) {
		if v, _, err := k.GetIntegerValue(name); err == nil {
			*c = color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
		}
	}
	percent := func(name string, p *float64) {
		if v, _, err := k.GetIntegerValue(name); err == nil {
			*p = float64(min(v, 100)) / 100
		}
	}
	rgb("ColorizationColor", &g.Color)
	rgb("ColorizationAfterglow", &g.Afterglow)
	percent("ColorizationColorBalance", &g.ColorBalance)
	percent("ColorizationAfterglowBalance", &g.AfterglowBalance)
	percent("ColorizationBlurBalance", &g.BlurBalance)
	percent("ColorizationGlassReflectionIntensity", &g.Reflection)
	if v, _, err := k.GetIntegerValue("ColorizationOpaqueBlend"); err == nil {
		g.Opaque = v != 0
	}
	return g
}

// themeName is the file of the current visual style, empty without one
// (the Classic theme).
func themeName() string {
	var name [themeNameLength]uint16
	if hr, _, _ := getCurrentThemeName.Call(uintptr(unsafe.Pointer(&name[0])), themeNameLength, 0, 0, 0, 0); hr != 0 {
		return ""
	}
	return windows.UTF16ToString(name[:])
}

// reflectionStreaks reads the streaks of the glass from the visual style in
// the file: part 40 of its class DWMWINDOW, in the atlas of the DWM's
// images. It is nil where the style has none, and without a style. hwnd is
// a window of the process: uxtheme serves a process that has one.
func reflectionStreaks(hwnd uintptr, file string) image.Image {
	if file == "" {
		return nil
	}
	class, err := windows.UTF16PtrFromString("DWMWINDOW")
	if err != nil {
		return nil
	}
	theme, _, _ := openThemeData.Call(hwnd, uintptr(unsafe.Pointer(class)))
	if theme == 0 {
		return nil
	}
	defer closeThemeData.Call(theme)
	var place windows.Rect
	if hr, _, _ := getThemeRect.Call(theme, reflectionPart, 0, tmtAtlasRect, uintptr(unsafe.Pointer(&place))); hr != 0 {
		return nil
	}
	// The atlas is a resource of the style's file, loaded as data.
	module, err := windows.LoadLibraryEx(file, 0, windows.LOAD_LIBRARY_AS_DATAFILE)
	if err != nil {
		return nil
	}
	defer windows.FreeLibrary(module)
	var data *byte
	var size uint32
	if hr, _, _ := getThemeStream.Call(theme, 0, 0, tmtDiskStream, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&size)), uintptr(module)); hr != 0 || data == nil || size == 0 {
		return nil
	}
	atlas, err := png.Decode(bytes.NewReader(bytes.Clone(unsafe.Slice(data, size))))
	if err != nil {
		return nil
	}
	r := image.Rect(int(place.Left), int(place.Top), int(place.Right), int(place.Bottom)).Intersect(atlas.Bounds())
	sub, ok := atlas.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if r.Empty() || !ok {
		return nil
	}
	return sub.SubImage(r)
}
