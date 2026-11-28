// SPDX-License-Identifier: Unlicense OR MIT

//go:build (!linux || !cgo) && !windows

package appwindow

func trimCHeap() {}
