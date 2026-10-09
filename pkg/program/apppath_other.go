// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package program

// appPath finds a program by name where the system keeps a list of them
// besides PATH: only Windows does.
func appPath(name string) (string, bool) { return "", false }
