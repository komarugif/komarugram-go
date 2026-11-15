// SPDX-License-Identifier: Unlicense OR MIT

//go:build !linux

package program

// Here nothing counts memory reserved but not used in what it bounds.
func reserved(string, []string) uint64 { return 0 }
