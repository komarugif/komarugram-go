// SPDX-License-Identifier: Unlicense OR MIT

// Package program runs the external programs the client hands work to — a
// video player, a browser — and asks them what they are before trusting a
// path the user picked: any file can be picked, and running it as a player
// would do whatever that file does.
package program

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// searchOff turns off looking for programs on the system: only the paths
// the user set are run. It is for trying the client as on a machine without
// them.
var searchOff atomic.Bool

// SetSearching turns looking for programs on the system on or off: off,
// LookPath and FindFlatpak find nothing.
func SetSearching(on bool) { searchOff.Store(!on) }

// Searching reports whether programs are looked for on the system.
func Searching() bool { return !searchOff.Load() }

// ErrSearchDisabled is LookPath's error while searching is off.
var ErrSearchDisabled = errors.New("looking for programs on the system is turned off")

// LookPath is exec.LookPath, unless searching is off; on Windows a program
// not on PATH is looked for in the App Paths too, where installers register
// it. Every search for an external program goes through it or FindFlatpak.
func LookPath(name string) (string, error) {
	if searchOff.Load() {
		return "", fmt.Errorf("%s: %w", name, ErrSearchDisabled)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		if found, ok := appPath(name); ok {
			return found, nil
		}
	}
	return path, err
}

// bannerTimeout bounds how long a program may take to print its version. A
// flatpak takes about a second the first time.
const bannerTimeout = 10 * time.Second

// bannerLimit is how much of the answer is kept: a version fits in a line.
const bannerLimit = 64 << 10

// ErrNoBanner means the program ran but printed nothing to go by.
var ErrNoBanner = errors.New("the program did not print its version")

// Banner runs name with args — normally --version — and returns the first
// line it printed. The program runs in the C locale, so that the words
// around the version are not translated, with nothing on its input, in a
// process group of its own that is killed as a whole when it takes too long.
func Banner(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, bannerTimeout)
	defer cancel()
	cmd := CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C")
	out := &limitedBuffer{limit: bannerLimit}
	cmd.Stdout = out
	Group(cmd)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s: no answer in %v", filepath.Base(name), bannerTimeout)
		}
		return "", err
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", ErrNoBanner
}

// limitedBuffer keeps the first limit bytes written to it and drops the rest,
// so that a program printing without end cannot fill the memory.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// FlatpakApp reports whether path is the launcher flatpak exports for an
// application — a script that runs "flatpak run … <id>" — and returns the id.
// A flatpak has its own /tmp, so a socket for it must be made where both
// sides see it: RuntimeDir.
func FlatpakApp(path string) (id string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	text := string(head[:n])
	if !strings.HasPrefix(text, "#!") {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if filepath.Base(fields[i]) != "flatpak" || fields[i+1] != "run" {
				continue
			}
			// The id is the first word after "run" that is not an option.
			for _, field := range fields[i+2:] {
				if !strings.HasPrefix(field, "-") {
					return field, strings.Count(field, ".") >= 2
				}
			}
		}
	}
	return "", false
}

// FlatpakExports are the directories flatpak puts its launchers in, the
// user's installation first.
func FlatpakExports() []string {
	var dirs []string
	if dir := os.Getenv("FLATPAK_USER_DIR"); dir != "" {
		dirs = append(dirs, filepath.Join(dir, "exports", "bin"))
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "flatpak", "exports", "bin"))
	}
	return append(dirs, "/var/lib/flatpak/exports/bin")
}

// FindFlatpak returns the launcher of the flatpak app id, or "".
func FindFlatpak(id string) string {
	if searchOff.Load() {
		return ""
	}
	for _, dir := range FlatpakExports() {
		path := filepath.Join(dir, id)
		if IsExecutable(path) {
			return path
		}
	}
	return ""
}

// RuntimeDir returns a directory that the flatpak app id and this process
// both see under the same path: flatpak shares $XDG_RUNTIME_DIR/app/<id>
// with the sandbox. It is "" when there is no runtime directory.
func RuntimeDir(id string) string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		return ""
	}
	dir := filepath.Join(base, "app", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// IsExecutable reports whether path is a regular file that can be run.
func IsExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return isExecutable(info)
}

// dottedVersion is a version from a Windows version resource with dots
// between its parts: some programs write the old form with commas, as VLC
// 3.0.24 does ("3,0,24,0").
func dottedVersion(v string) string {
	return strings.NewReplacer(", ", ".", ",", ".").Replace(strings.TrimSpace(v))
}
