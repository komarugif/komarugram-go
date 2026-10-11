// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bytes"
	"io"
	"os"
)

const appImageHubNotice = "Please consider submitting your AppImage to AppImageHub, the crowd-sourced\n" +
	"central directory of available AppImages, by opening a pull request\n" +
	"at https://github.com/AppImage/appimage.github.io\n"

// noticeFilter removes only the complete upstream notice, even when it is
// split across writes. All other bytes pass through, including progress bars
// and incomplete or changed notices. Buffer at most one partial notice.
type noticeFilter struct {
	out     io.Writer
	pending []byte
}

func (f *noticeFilter) Write(p []byte) (int, error) {
	data := append(f.pending, p...)
	for {
		if i := bytes.Index(data, []byte(appImageHubNotice)); i >= 0 {
			if err := f.emit(data[:i]); err != nil {
				return 0, err
			}
			data = data[i+len(appImageHubNotice):]
			continue
		}
		keep := min(len(data), len(appImageHubNotice)-1)
		for keep > 0 && !bytes.Equal(data[len(data)-keep:], []byte(appImageHubNotice[:keep])) {
			keep--
		}
		if err := f.emit(data[:len(data)-keep]); err != nil {
			return 0, err
		}
		f.pending = append(f.pending[:0], data[len(data)-keep:]...)
		return len(p), nil
	}
}

func (f *noticeFilter) emit(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	n, err := f.out.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return err
}

func (f *noticeFilter) Flush() error {
	err := f.emit(f.pending)
	f.pending = nil
	return err
}

func runPackager(name string, args []string, dir string, env []string) error {
	filtered := &noticeFilter{out: os.Stderr}
	err := runWithStderr(name, args, dir, env, filtered)
	flushErr := filtered.Flush()
	if err != nil {
		return err
	}
	return flushErr
}
