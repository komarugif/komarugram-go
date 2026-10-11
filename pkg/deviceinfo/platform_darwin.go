// SPDX-License-Identifier: Unlicense OR MIT

package deviceinfo

import "golang.org/x/sys/unix"

// platform is the version of macOS and of its Darwin kernel.
func platform() (string, string) {
	name := "macOS"
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil && v != "" {
		name += " " + v
	}
	kernel := ""
	if v, err := unix.Sysctl("kern.osrelease"); err == nil && v != "" {
		kernel = "Darwin " + v
	}
	return name, kernel
}
