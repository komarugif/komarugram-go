// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package wallpaper

// AeroGlass reports whether the windows' own blur draws Aero's glass: on
// Windows 7 only.
func AeroGlass() bool { return false }

// SystemWindowColor is the color of the windows Windows 7 has; there is none
// here.
func SystemWindowColor() uint32 { return 0 }
