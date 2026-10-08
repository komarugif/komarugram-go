// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package app

// FrameBlurs reports whether the blur behind a window is drawn behind the
// content of a window with the system's frame too: on Windows 7 only.
func FrameBlurs() bool { return false }
