// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows && !(linux && cgo && !novulkan)

package hwvideo

// newDevice makes the video decoder of the GPU: Direct3D 11 on Windows,
// Vulkan Video on Linux, none elsewhere.
func newDevice() (device, error) {
	return nil, unsupported("no hardware decoder on this system")
}

// Available reports whether this system has a hardware decoder to try.
func Available() bool { return false }
