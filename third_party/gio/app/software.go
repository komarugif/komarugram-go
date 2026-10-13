// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"sync/atomic"

	"gioui.org/gpu"
)

// softwareRendered is whether the GPU made last for a window draws on the CPU.
var softwareRendered atomic.Bool

// SoftwareRendered reports whether windows are drawn by a software
// rasterizer (llvmpipe and the other software renderers of OpenGL, WARP of
// Direct3D) rather than a GPU, as far as the GPU made last for a window
// tells: false until a window has drawn. A window that finds it changed is
// invalidated, so whatever it decides from it is decided again.
func SoftwareRendered() bool { return softwareRendered.Load() }

func gpuSoftware(g gpu.GPU) bool { return gpu.Software(g) }
