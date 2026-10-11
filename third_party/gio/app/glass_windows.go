// SPDX-License-Identifier: Unlicense OR MIT

package app

import "gioui.org/app/internal/windows"

// FrameBlurs reports whether the blur behind a window (BlurBehind) is drawn
// behind the content of a window with the system's frame too: Aero's glass
// on Windows 7 is. Acrylic, of Windows 10 and 11, is drawn around such a
// window, and blurs behind the content of one without the frame only.
func FrameBlurs() bool { return windows.GlassBlur() }
