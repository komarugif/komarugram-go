// SPDX-License-Identifier: Unlicense OR MIT

// Package install puts the messenger into the system, as an installer
// would, and takes it out again: the program copies itself to a folder,
// registers itself in the system's list of applications (Windows' list of
// installed programs, a .desktop file on Linux), and makes its shortcuts
// on the desktop and in the Start menu or the applications menu. The
// registration's uninstall command is the program itself with -uninstall.
//
// It is for as long as the program has no digital signature and so no
// installer of its own: a user can also decline, and run the file as it
// is. The user's data, in the configuration and cache directories, stay
// where they are whichever copy runs.
package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// appName is the name of the folder the program goes to, and of its
	// shortcut on the desktop.
	appName = "KomaruGram"
	// listName is the program's name in the system's list of programs and
	// in the Start menu or the applications menu.
	listName = "KomaruGram Go"
	// id names the registration, the desktop file and the user's data
	// directories.
	id = "komarugram-go"
)

// The arguments with which the program, run with the administrator's
// rights, does what the user's own process cannot: copy itself into a
// folder of the system and register itself for every user, or undo that
// (Windows).
const (
	FlagInstallSystem   = "install-system"
	FlagUninstallSystem = "uninstall-system"
)

// ErrNeedsAdmin is the reason a folder cannot be installed to or removed
// from without rights the user does not have and the system cannot ask
// for (Linux: no elevation is offered).
var ErrNeedsAdmin = errors.New("install: no permission to write to the folder")

// ErrCancelled is returned when the user refused the administrator's
// rights the system asked for.
var ErrCancelled = errors.New("install: the administrator's rights were refused")

// ErrInUse is returned when the program to remove is in use: on Windows,
// a copy of it still runs.
var ErrInUse = errors.New("install: the program is in use")

// Options are what to install, and where.
type Options struct {
	// Dir is the folder the program is copied to; it is made if missing.
	Dir string
	// Desktop makes a shortcut on the desktop; Menu an entry in the Start
	// menu or the applications menu.
	Desktop, Menu bool
}

// Installation is a copy of the program installed in the system.
type Installation struct {
	Dir, Exe string
	// System is set for a copy registered for every user of the machine,
	// in a folder only an administrator can change (Windows).
	System bool
	// MadeFolder is set when the installation made Dir: only then does
	// the removal remove it, and only if nothing else is in it. A folder
	// the user had, even one left empty, stays.
	MadeFolder bool
}

// Supported reports whether the program can install itself on this
// system: on Windows and Linux.
func Supported() bool { return supported }

// Offered reports whether the sign-in should offer to install: on a
// system where it can, for a program that is not the installed copy and
// was not put on the system otherwise (a package, a Flatpak, an AppImage,
// `go run`).
func Offered() bool {
	if !supported {
		return false
	}
	self, err := executable()
	if err != nil || managed(self) {
		return false
	}
	if inst, ok := Find(); ok && sameFile(inst.Exe, self) {
		return false
	}
	return true
}

// Running reports whether this process is the installed copy.
func Running() bool {
	self, err := executable()
	if err != nil {
		return false
	}
	inst, ok := Find()
	return ok && sameFile(inst.Exe, self)
}

// managed reports whether exe was put on the system by something that
// keeps it up to date or removes it itself, or is a build of `go run`.
func managed(exe string) bool {
	slash := filepath.ToSlash(exe)
	if strings.Contains(slash, "/go-build") {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	for _, env := range []string{"FLATPAK_ID", "SNAP", "APPIMAGE"} {
		if os.Getenv(env) != "" {
			return true
		}
	}
	for _, prefix := range []string{"/usr/", "/opt/", "/nix/", "/snap/", "/gnu/"} {
		if strings.HasPrefix(slash, prefix) {
			return true
		}
	}
	return false
}

// DefaultDir is the folder offered first: the folder of the installed
// copy if there is one, or else one in the user's own profile, which
// needs no administrator's rights.
func DefaultDir() string {
	if inst, ok := Find(); ok {
		return inst.Dir
	}
	return userDir()
}

// FolderFor is the folder to install to when the user chose dir in the
// system's folder chooser: the program's own folder in it, unless dir is
// that folder already. Choosing "Program Files" means a folder in it.
func FolderFor(dir string) string {
	if dir == "" {
		return ""
	}
	base := filepath.Base(dir)
	if strings.EqualFold(base, appName) || strings.EqualFold(base, id) {
		return dir
	}
	return filepath.Join(dir, appName)
}

// Install copies the program to o.Dir, registers it and makes the
// shortcuts o asks for. It returns the path of the installed program, to
// start in place of this one.
func Install(ctx context.Context, o Options) (string, error) {
	if !supported {
		return "", errors.ErrUnsupported
	}
	dir, err := cleanDir(o.Dir)
	if err != nil {
		return "", err
	}
	self, err := executable()
	if err != nil {
		return "", err
	}
	return install(ctx, self, dir, o)
}

// Uninstall removes inst: the program, its folder if nothing else is left
// in it, the registration and the shortcuts; with data, also the user's
// data (accounts, sessions, history, settings). The sessions stay on
// Telegram's servers: removing their keys here does not end them.
func Uninstall(ctx context.Context, inst Installation, data bool) error {
	if !supported {
		return errors.ErrUnsupported
	}
	if err := uninstall(ctx, inst); err != nil {
		return err
	}
	if data {
		return removeData()
	}
	return nil
}

// cleanDir checks that dir is a folder the program can be installed to.
func cleanDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return "", fmt.Errorf("install: %q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if filepath.Dir(dir) == dir {
		return "", fmt.Errorf("install: %q is the root of a disk", dir)
	}
	return dir, nil
}

// copyProgram copies the program src to exe, through a temporary file
// beside it so that a failed copy leaves no half of a program.
func copyProgram(src, exe string) error {
	if sameFile(src, exe) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(exe), "."+filepath.Base(exe)+".*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), exe)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// removeProgram removes the files the installation put in its folder,
// then the folder, if the installation made it and nothing else is in it:
// never what the user keeps there, nor a folder of theirs, such as one
// they made for the program or their desktop, chosen by mistake.
func removeProgram(inst Installation, files ...string) error {
	for _, name := range append([]string{filepath.Base(inst.Exe)}, files...) {
		if err := os.Remove(filepath.Join(inst.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if !inst.MadeFolder {
		return nil
	}
	entries, err := os.ReadDir(inst.Dir)
	if errors.Is(err, os.ErrNotExist) || err == nil && len(entries) > 0 {
		return nil
	}
	// The system removes only an empty folder, whatever was read above.
	if err := os.Remove(inst.Dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// madeFolder reports whether installing to dir makes the folder: it does
// not exist yet, or the copy installed there made it.
func madeFolder(dir string, existing Installation, installed bool) bool {
	if installed && sameFolderPath(existing.Dir, dir) {
		return existing.MadeFolder
	}
	_, err := os.Stat(dir)
	return errors.Is(err, os.ErrNotExist)
}

// sameFolderPath reports whether a and b name one folder by their text;
// Windows' names do not tell case.
func sameFolderPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// removeData removes the user's data: the configuration and the cache
// directories of the program.
func removeData() error {
	var errs []error
	for _, base := range []func() (string, error){os.UserConfigDir, os.UserCacheDir} {
		dir, err := base()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, id)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// executable is the path of this program, links resolved.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// sameFile reports whether a and b are one file.
func sameFile(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}

// writable reports whether files can be made in dir, or, for a folder not
// made yet, in the nearest folder above it that exists.
func writable(dir string) error {
	for {
		if info, err := os.Stat(dir); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("install: %s is not a folder", dir)
			}
			f, err := os.CreateTemp(dir, ".komarugram-*")
			if err != nil {
				if errors.Is(err, os.ErrPermission) {
					return ErrNeedsAdmin
				}
				return err
			}
			f.Close()
			return os.Remove(f.Name())
		}
		up := filepath.Dir(dir)
		if up == dir {
			return fmt.Errorf("install: no folder of %s exists", dir)
		}
		dir = up
	}
}

// NeedsAdmin reports whether installing to dir needs an administrator's
// rights: on Windows the system then asks for them, and on Linux the
// folder cannot be used.
func NeedsAdmin(dir string) bool {
	dir, err := cleanDir(dir)
	return err == nil && errors.Is(writable(dir), ErrNeedsAdmin)
}
