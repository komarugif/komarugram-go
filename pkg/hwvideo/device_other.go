// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package hwvideo

// newDevice makes the video decoder of the GPU. Only Windows has one so
// far; Vulkan Video, for Linux, is to come (docs/HW_VIDEO.md).
func newDevice() (device, error) {
	return nil, unsupported("no hardware decoder on this system")
}

// Available reports whether this system has a hardware decoder to try.
func Available() bool { return false }
