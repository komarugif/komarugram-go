// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"context"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// A console program the client starts opens no console window of its own.
func TestCommandHasNoConsoleWindow(t *testing.T) {
	for _, cmd := range []*exec.Cmd{Command("ffmpeg"), CommandContext(context.Background(), "ffprobe")} {
		if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
			t.Errorf("%s would open a console window", cmd.Args[0])
		}
	}
}
