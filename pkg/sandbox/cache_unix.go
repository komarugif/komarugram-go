// SPDX-License-Identifier: Unlicense OR MIT

//go:build unix

package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// openNoFollow opens path for reading, failing on a symbolic link.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// ownedPrivately checks that st is this user's and that no one else can
// write it, or, for a secret, read it either.
func ownedPrivately(st fs.FileInfo, secret bool) error {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("no owner to check")
	}
	if int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to user %d", st.Name(), sys.Uid)
	}
	mask := fs.FileMode(0o022)
	if secret {
		mask = 0o077
	}
	if st.Mode().Perm()&mask != 0 {
		return fmt.Errorf("%s is open to others (%v)", st.Name(), st.Mode().Perm())
	}
	return nil
}

// privateDir makes dir, or checks the one there: this user's directory, not
// a link, closed to others; one open to others is closed.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not this user's", dir)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
