// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"context"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// noWindow keeps a console program from opening a console window. A
// program with windows of its own, a player or a browser, still shows them.
func noWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}

// Group does nothing on Windows: there is no flatpak to leave a program
// behind, and killing the process is enough.
func Group(cmd *exec.Cmd) {}

// KillGroup kills the process cmd started.
func KillGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func isExecutable(info fs.FileInfo) bool {
	return strings.EqualFold(filepath.Ext(info.Name()), ".exe")
}

// FileVersion reads the product name and version a Windows program carries
// in its version resource, such as "VLC media player" and "3.0.20", without
// running it: a GUI program on Windows prints nothing for --version, and a
// browser opens a window.
func FileVersion(ctx context.Context, path string) (product, version string, err error) {
	ctx, cancel := context.WithTimeout(ctx, bannerTimeout)
	defer cancel()
	// The path goes in through the environment, never into the script.
	cmd := CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		`$v = (Get-Item -LiteralPath $env:KITCHEN_PROGRAM).VersionInfo; $v.ProductName; $v.ProductVersion`)
	cmd.Env = append(cmd.Environ(), "KITCHEN_PROGRAM="+path)
	out := &limitedBuffer{limit: bannerLimit}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.ReplaceAll(out.String(), "\r", ""), "\n")
	if len(lines) < 2 {
		return "", "", ErrNoBanner
	}
	return strings.TrimSpace(lines[0]), dottedVersion(lines[1]), nil
}
