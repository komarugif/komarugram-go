// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package app

// GPUFailed reports whether the GPU's driver failed to draw, and windows are
// drawn in software instead: only Windows turns to software, WARP, on its
// own.
func GPUFailed() bool { return false }
