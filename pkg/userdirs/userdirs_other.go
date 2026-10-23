// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package userdirs

import (
	"path/filepath"
	"runtime"
)

// xdg reports whether the system names its directories in user-dirs.dirs;
// macOS's are ~/Downloads and ~/Pictures (NSDownloadsDirectory, localized
// in Finder's display only), and Haiku has no downloads directory of its
// own (WebPositive saves to the desktop).
func xdg() bool {
	switch runtime.GOOS {
	case "darwin", "ios", "haiku", "android":
		return false
	}
	return true
}

func downloads() string {
	if xdg() {
		return xdgUserDir("DOWNLOAD")
	}
	return ""
}

func pictures() string {
	if xdg() {
		return xdgUserDir("PICTURES")
	}
	return ""
}

func desktop(home string) string {
	if xdg() {
		if dir := xdgUserDir("DESKTOP"); dir != "" {
			return dir
		}
	}
	return filepath.Join(home, "Desktop")
}

func safeRoot(home string) string { return home }
