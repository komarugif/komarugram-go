// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package alert

import (
	"errors"
	"komarugram/pkg/program"
	"os/exec"
	"runtime"
)

// show asks the first program there is for the box: the desktop's own
// dialogs, as the file chooser does, then X11's.
func show(title, text string) bool {
	commands := [][]string{
		{"kdialog", "--title", title, "--error", text},
		{"zenity", "--error", "--no-markup", "--title", title, "--text", text},
		{"xmessage", "-center", title + "\n\n" + text},
	}
	if runtime.GOOS == "darwin" {
		commands = [][]string{{"osascript",
			"-e", "on run argv",
			"-e", "display alert (item 1 of argv) message (item 2 of argv) as critical",
			"-e", "end run", title, text}}
	}
	for _, c := range commands {
		path, err := exec.LookPath(c[0])
		if err != nil {
			continue
		}
		// The dialog's exit status tells how it was closed, not whether it
		// was shown: a program that ran has shown it.
		err = program.Command(path, c[1:]...).Run()
		if exited := new(exec.ExitError); err == nil || errors.As(err, &exited) {
			return true
		}
	}
	return false
}
