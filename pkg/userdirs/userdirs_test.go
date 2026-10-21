// SPDX-License-Identifier: Unlicense OR MIT

package userdirs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// user-dirs.dirs is read as xdg-user-dirs reads it.
func TestParseUserDirs(t *testing.T) {
	const home = "/home/u"
	for _, c := range []struct{ file, want string }{
		{`XDG_DOWNLOAD_DIR="$HOME/Завантаження"`, "/home/u/Завантаження"},
		{"  \tXDG_DOWNLOAD_DIR \t= \t\"/data/dl\"", "/data/dl"},
		{`XDG_DOWNLOAD_DIR="$HOME/with \"quote\" and \\ slash"`, `/home/u/with "quote" and \ slash`},
		{"XDG_DOWNLOAD_DIR=\"$HOME/a\"\nXDG_DOWNLOAD_DIR=\"$HOME/b\"", "/home/u/b"},
		// A relative path, or no quotes, is passed over.
		{`XDG_DOWNLOAD_DIR="Downloads"`, ""},
		{`XDG_DOWNLOAD_DIR=$HOME/Downloads`, ""},
		{"# XDG_DOWNLOAD_DIR=\"/x\"\nXDG_DOWNLOADS_DIR=\"/y\"\nXDG_DESKTOP_DIR=\"/z\"", ""},
		{`XDG_DOWNLOAD_DIR="$HOME/"`, "/home/u/"},
	} {
		if got := parseUserDirs(strings.NewReader(c.file), "DOWNLOAD", home); got != c.want {
			t.Errorf("%q: %q, want %q", c.file, got, c.want)
		}
	}
}

// The downloads directory is the one user-dirs.dirs names, but for the home
// directory or the desktop, which give ~/Downloads.
func TestDownloads(t *testing.T) {
	if !xdg() {
		t.Skip("user-dirs.dirs is not read here")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "conf"))
	os.MkdirAll(filepath.Join(home, "conf"), 0o700)
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(home, "conf", "user-dirs.dirs"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ file, want string }{
		{"XDG_DOWNLOAD_DIR=\"$HOME/Завантаження\"\n", filepath.Join(home, "Завантаження")},
		{"XDG_DOWNLOAD_DIR=\"$HOME/\"\n", filepath.Join(home, "Downloads")},
		{"", filepath.Join(home, "Downloads")},
		{"XDG_DESKTOP_DIR=\"$HOME/Стільниця\"\nXDG_DOWNLOAD_DIR=\"$HOME/Стільниця\"\n", filepath.Join(home, "Downloads")},
		{"XDG_DOWNLOAD_DIR=\"$HOME/Desktop\"\n", filepath.Join(home, "Downloads")},
	} {
		write(c.file)
		if got := Downloads(); got != c.want {
			t.Errorf("%q: %q, want %q", c.file, got, c.want)
		}
	}
}
