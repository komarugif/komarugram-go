// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"os"

	"golang.org/x/sys/unix"
)

// MemoryFile is a file in memory holding data, never on a disk, to hand
// to FFmpeg open (RowInput.File): it reads it, and seeks in it to loop,
// itself. It stays open while a player of it plays, for each FFmpeg the
// player starts; the caller closes it after Stop.
func MemoryFile(name string, data []byte) (*os.File, error) {
	fd, err := unix.MemfdCreate(name, unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
