// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	gioapp "gioui.org/app"

	"komarugram/pkg/miniapp"
	"komarugram/pkg/player"
)

// A browser driven for Mini Apps and the player, and VLC, draw on the same
// driver as the client's windows: where that failed, they draw without it.
func init() {
	miniapp.GPUFailed = gioapp.GPUFailed
	player.GPUFailed = gioapp.GPUFailed
}
