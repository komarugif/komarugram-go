// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows && !haiku && !(((linux && !android) || freebsd || openbsd) && !nox11 && cgo)

package appwindow

import (
	"gioui.org/io/event"

	"komarugram/internal/wallpaper"
)

// desktopView finds no native window to follow over the desktop here.
func desktopView(event.Event) (wallpaper.View, bool) { return wallpaper.View{}, false }
