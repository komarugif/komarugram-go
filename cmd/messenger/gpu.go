// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	gioapp "gioui.org/app"

	"komarugram/pkg/miniapp"
)

// A browser driven for Mini Apps and the player draws on the same driver as
// the client's windows: where that failed, it runs without the GPU.
func init() { miniapp.GPUFailed = gioapp.GPUFailed }
