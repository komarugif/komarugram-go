// SPDX-License-Identifier: Unlicense OR MIT

//go:build freebsd || openbsd || netbsd || dragonfly

package deviceinfo

import "golang.org/x/sys/unix"

// platform is the system and its release, which is its kernel's.
func platform() (string, string) {
	var u unix.Utsname
	if unix.Uname(&u) != nil {
		return "", ""
	}
	return unix.ByteSliceToString(u.Sysname[:]), unix.ByteSliceToString(u.Release[:])
}
