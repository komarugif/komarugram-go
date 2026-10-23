// SPDX-License-Identifier: Unlicense OR MIT

package userdirs

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// known is the known folder id, which the user may have moved, or "".
func known(id *windows.KNOWNFOLDERID) string {
	dir, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return dir
}

// xdg reports whether the system names its directories in user-dirs.dirs.
func xdg() bool { return false }

func downloads() string { return known(windows.FOLDERID_Downloads) }
func pictures() string  { return known(windows.FOLDERID_Pictures) }

func desktop(home string) string {
	if dir := known(windows.FOLDERID_Desktop); dir != "" {
		return dir
	}
	return filepath.Join(home, "Desktop")
}

// safeRoot is where a safe directory is made: the documents, as Chromium's
// GetUserDownloadsDirectorySafe has it (not localized: the name is the
// client's, whatever language the system speaks).
func safeRoot(home string) string {
	if dir := known(windows.FOLDERID_Documents); dir != "" {
		return dir
	}
	return home
}
