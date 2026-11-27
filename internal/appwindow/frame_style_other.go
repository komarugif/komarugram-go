// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package appwindow

import "image/color"

func applyFrameStyle(uintptr, bool, bool, color.NRGBA) {}
