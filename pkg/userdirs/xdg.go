// SPDX-License-Identifier: Unlicense OR MIT

package userdirs

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// xdgUserDir is directory kind (DOWNLOAD, PICTURES, DESKTOP) as
// $XDG_CONFIG_HOME/user-dirs.dirs names it, or "" when it does not: the
// lookup of xdg-user-dirs, which Chromium carries. A line is
// XDG_<kind>_DIR="$HOME/…" or an absolute path in quotes, a backslash
// keeping the character after it; the last line of a kind wins.
func xdgUserDir(kind string) string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	f, err := os.Open(filepath.Join(config, "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	defer f.Close()
	return parseUserDirs(f, kind, home)
}

func parseUserDirs(r io.Reader, kind, home string) string {
	found := ""
	scan := bufio.NewScanner(r)
	for scan.Scan() {
		p := strings.TrimLeft(scan.Text(), " \t")
		var ok bool
		if p, ok = strings.CutPrefix(p, "XDG_"+kind+"_DIR"); !ok {
			continue
		}
		p = strings.TrimLeft(p, " \t")
		if p, ok = strings.CutPrefix(p, "="); !ok {
			continue
		}
		p = strings.TrimLeft(p, " \t")
		if p, ok = strings.CutPrefix(p, `"`); !ok {
			continue
		}
		var dir strings.Builder
		if rest, relative := strings.CutPrefix(p, "$HOME/"); relative {
			dir.WriteString(home + "/")
			p = rest
		} else if !strings.HasPrefix(p, "/") {
			continue
		}
		for i := 0; i < len(p) && p[i] != '"'; i++ {
			if p[i] == '\\' && i+1 < len(p) {
				i++
			}
			dir.WriteByte(p[i])
		}
		found = dir.String()
	}
	return found
}
