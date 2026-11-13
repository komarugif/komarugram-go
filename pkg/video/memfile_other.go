// SPDX-License-Identifier: Unlicense OR MIT

//go:build !linux

package video

import (
	"errors"
	"os"
)

// MemoryFile is a file in memory holding data where the system has one
// that FFmpeg can open by a path, Linux's memfd; elsewhere it fails, and
// the caller serves the data another way.
func MemoryFile(name string, data []byte) (*os.File, error) {
	return nil, errors.ErrUnsupported
}
