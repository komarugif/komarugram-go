// SPDX-License-Identifier: Unlicense OR MIT

package player

import "golang.org/x/sys/windows"

// unsupported reports whether k does not run on this Windows: mpv's
// builds need Windows 8.1 (SHCORE.dll), and none was found for Windows 7
// (docs/PLATFORMS.md).
func unsupported(k Kind) bool {
	v := windows.RtlGetVersion()
	return k == MPV && !atLeast81(v.MajorVersion, v.MinorVersion)
}

// atLeast81 reports whether Windows major.minor is 8.1 (6.3) or later.
func atLeast81(major, minor uint32) bool {
	return major > 6 || major == 6 && minor >= 3
}
