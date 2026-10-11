// SPDX-License-Identifier: Unlicense OR MIT

package deviceinfo

import (
	"os"

	"golang.org/x/sys/unix"
)

// platform is the distribution, from os-release, and Linux's release.
func platform() (string, string) {
	name := "Linux"
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if data, err := os.ReadFile(path); err == nil {
			if n := osReleaseName(string(data)); n != "" {
				name = n
			}
			break
		}
	}
	var u unix.Utsname
	if unix.Uname(&u) != nil {
		return name, ""
	}
	return name, "Linux " + unix.ByteSliceToString(u.Release[:])
}
