// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package player

// unsupported reports whether k does not run here: every kind does.
func unsupported(Kind) bool { return false }
