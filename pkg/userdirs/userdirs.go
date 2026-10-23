// SPDX-License-Identifier: Unlicense OR MIT

// Package userdirs finds the user's own directories, downloads and
// pictures, as Chromium does (chrome/common/chrome_paths_*.cc,
// base/nix/xdg_util.cc, chrome/browser/download/download_prefs.cc): the
// directory the system names, which the user may have moved or a locale
// named in its language, and a safe one instead of the home directory or
// the desktop.
package userdirs

import (
	"os"
	"path/filepath"
)

// Downloads is the user's downloads directory. It may not exist yet.
func Downloads() string {
	return safe(downloads(), "Downloads")
}

// Pictures is the user's pictures directory. It may not exist yet.
func Pictures() string {
	return safe(pictures(), "Pictures")
}

// safe is dir, unless it is none, the home directory or the desktop, where
// files would land among the user's own: then fallback in the home
// directory (in the documents on Windows), as Chromium's
// DownloadPathIsDangerous has it.
func safe(dir, fallback string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		if dir != "" {
			return dir
		}
		return os.TempDir()
	}
	if dir != "" {
		dir = filepath.Clean(dir)
		if !same(dir, home) && !same(dir, desktop(home)) {
			return dir
		}
	}
	return filepath.Join(safeRoot(home), fallback)
}

// same reports whether a and b name one directory.
func same(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}
