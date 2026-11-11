// SPDX-License-Identifier: Unlicense OR MIT

//go:build !(haiku && cgo)

// Package haikumem tells how much memory a team of Haiku keeps in RAM; see
// haikumem_haiku.go. Without cgo, and anywhere but Haiku, it tells nothing.
package haikumem

// TeamMemory tells nothing here: the process is taken to be gone, and nothing
// watches it.
func TeamMemory(int) (uint64, bool) { return 0, false }
