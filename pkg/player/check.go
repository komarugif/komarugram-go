// SPDX-License-Identifier: Unlicense OR MIT

package player

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/miniapp"
	"komarugram/pkg/program"
)

var (
	// ErrNotExecutable means the path is not a program that can be run.
	ErrNotExecutable = errors.New("not an executable file")
	// ErrWrongProgram means the program is not the player it was picked as.
	ErrWrongProgram = errors.New("the program is not the player")
	// ErrUnsupportedSystem means the player cannot be driven on this system.
	ErrUnsupportedSystem = errors.New("the player cannot be driven on this system")
	// ErrSnap means the player is a snap, which is not supported: its /tmp
	// is its own, and it shares no directory for a socket with this process.
	ErrSnap = errors.New("players installed as snaps are not supported")
)

// VersionError is a player of a version this package cannot drive.
type VersionError struct {
	Kind    Kind
	Version string
	// Want says which versions work, such as "3.x".
	Want string
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("%s %s is not supported, %s is needed", e.Kind.Title(), e.Version, e.Want)
}

var (
	mpvBanner = regexp.MustCompile(`^mpv v?(\d+)\.(\d+)(\.\d+)?`)
	vlcBanner = regexp.MustCompile(`^VLC (?:media player )?version (\d+\.\d+\.\d+)`)
)

// Check makes sure the program at path is the player kind, in a version
// this package can drive, and returns what it is, such as "VLC 3.0.20".
//
// It runs the program with --version, so it is for a path the user picked
// and trusts to be a program, not for any file: on Windows, where players
// print nothing for --version, it reads the version resource instead.
func Check(ctx context.Context, kind Kind, path string) (string, error) {
	if kind == Chromium {
		return miniapp.CheckBrowser(ctx, path)
	}
	if !filepath.IsAbs(path) || !program.IsExecutable(path) {
		return "", ErrNotExecutable
	}
	if isSnap(path) {
		return "", ErrSnap
	}
	if runtime.GOOS == "windows" {
		return checkWindows(ctx, kind, path)
	}
	banner, err := program.Banner(ctx, path, "--version")
	if err != nil {
		return "", err
	}
	return parseBanner(kind, banner)
}

// isSnap reports whether path runs a snap: /snap/bin holds links to the
// snap launcher.
func isSnap(path string) bool {
	if strings.HasPrefix(path, "/snap/") {
		return true
	}
	real, err := filepath.EvalSymlinks(path)
	return err == nil && (strings.HasPrefix(real, "/snap/") || filepath.Base(real) == "snap")
}

// checkKey identifies a program file as it was when it was checked.
type checkKey struct {
	kind    Kind
	path    string
	size    int64
	modTime time.Time
}

type checkResult struct {
	about string
	err   error
}

var (
	checksMu sync.Mutex
	checks   = map[checkKey]checkResult{}
)

// checked is Check, asked once for each state of the file.
func checked(ctx context.Context, kind Kind, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", ErrNotExecutable
	}
	key := checkKey{kind, path, info.Size(), info.ModTime()}
	checksMu.Lock()
	result, ok := checks[key]
	checksMu.Unlock()
	if ok {
		return result.about, result.err
	}
	about, err := Check(ctx, kind, path)
	// A timeout or a cancelled context says nothing about the file.
	if ctx.Err() == nil {
		checksMu.Lock()
		checks[key] = checkResult{about, err}
		checksMu.Unlock()
	}
	return about, err
}

// parseBanner reads the first line a player printed for --version.
func parseBanner(kind Kind, banner string) (string, error) {
	switch kind {
	case MPV:
		m := mpvBanner.FindStringSubmatch(banner)
		if m == nil {
			return "", ErrWrongProgram
		}
		version := m[1] + "." + m[2] + m[3]
		// --input-ipc-server, which drives it, came in 0.17.
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		if major == 0 && minor < 17 {
			return "", &VersionError{Kind: MPV, Version: version, Want: "0.17"}
		}
		return "mpv " + version, nil
	case VLC:
		m := vlcBanner.FindStringSubmatch(banner)
		if m == nil {
			return "", ErrWrongProgram
		}
		return checkVLCVersion(m[1])
	}
	return "", ErrWrongProgram
}

// checkVLCVersion accepts VLC 3, the one whose remote control interface
// (oldrc) has been tried; VLC 4 reworks the interfaces.
func checkVLCVersion(version string) (string, error) {
	if !strings.HasPrefix(version, "3.") {
		return "", &VersionError{Kind: VLC, Version: version, Want: "3.x"}
	}
	return "VLC " + version, nil
}

func checkWindows(ctx context.Context, kind Kind, path string) (string, error) {
	if kind == MPV {
		// mpv prints its version as elsewhere, and carries no version
		// resource.
		banner, err := program.Banner(ctx, path, "--version")
		if err != nil {
			return "", err
		}
		return parseBanner(kind, banner)
	}
	product, version, err := program.FileVersion(ctx, path)
	if err != nil {
		return "", err
	}
	if !strings.Contains(product, "VLC") {
		return "", ErrWrongProgram
	}
	return checkVLCVersion(version)
}
