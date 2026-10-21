// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows && !linux && !(darwin && cgo) && !(haiku && cgo)

package program

import "os/exec"

// Here nothing bounds the program's memory, nor watches it.
func startLimited(cmd *exec.Cmd, _ Limits, _ func()) error {
	return cmd.Start()
}
