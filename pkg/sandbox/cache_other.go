// SPDX-License-Identifier: Unlicense OR MIT

//go:build !unix

package sandbox

import (
	"fmt"
	"io/fs"
	"os"
)

// openNoFollow opens path for reading, failing on a symbolic link or another
// reparse point. The check comes before the open; that a link could be put
// in between does not matter, as entries are signed and read with a bound.
func openNoFollow(path string) (*os.File, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&fs.ModeSymlink != 0 || st.Mode()&fs.ModeIrregular != 0 {
		return nil, fmt.Errorf("%s is a link", path)
	}
	return os.Open(path)
}

// ownedPrivately has nothing to check here: on Windows the cache and the key
// live in the user's profile, whose ACL keeps other users out, and the
// entries are signed.
func ownedPrivately(fs.FileInfo, bool) error { return nil }

// privateDir makes dir, or checks that the one there is a directory and not
// a link.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}
