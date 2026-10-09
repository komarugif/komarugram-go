// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package program

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"syscall"
)

// noWindow does nothing: only Windows opens a console for a child.
func noWindow(cmd *exec.Cmd) {}

// Group starts cmd in a process group of its own and makes cancelling its
// context kill the whole group. A flatpak is bwrap running the program, and
// killing bwrap alone leaves the program running with no parent.
func Group(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return KillGroup(cmd) }
}

// KillGroup kills the process group cmd started, which Group made.
func KillGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func isExecutable(info fs.FileInfo) bool {
	return info.Mode().Perm()&0o111 != 0
}

// FileVersion reads a Windows program's version resource; elsewhere programs
// are asked with Banner.
func FileVersion(ctx context.Context, path string) (product, version string, err error) {
	return "", "", errors.ErrUnsupported
}
