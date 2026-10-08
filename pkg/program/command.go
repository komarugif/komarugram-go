// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"context"
	"os/exec"
)

// Command is exec.Command for a program the client starts: on Windows a
// console program, ffmpeg or powershell say, gets no console window. The
// client has no console there, and Windows opens one for each such child,
// a window that flashes up for every probe of a file. Every program the
// client runs is started through Command or CommandContext.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	noWindow(cmd)
	return cmd
}

// CommandContext is exec.CommandContext, as Command is exec.Command.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	noWindow(cmd)
	return cmd
}
