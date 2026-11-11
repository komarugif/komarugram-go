// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package program

import (
	"os/exec"
	"sync/atomic"
	"syscall"
)

// teamMemory is how the RAM a program keeps is told, set by SetTeamMemory.
// It is a package's, pkg/program/haikumem's, which has cgo: this package has
// none, so that what imports it only, as cmd/haiku-build does, is linked by
// Haiku's ld without a flag.
var teamMemory atomic.Pointer[func(pid int) (uint64, bool)]

// SetTeamMemory sets how the memory a program keeps in RAM is told, and
// whether it is still there. What starts programs with a bound imports
// pkg/program/haikumem for it; without one, none is bound.
func SetTeamMemory(used func(pid int) (uint64, bool)) {
	teamMemory.Store(&used)
}

// MemoryWatched tells whether the memory of a program is watched: whether
// SetTeamMemory was called, as what starts programs with a bound does. It is for
// their tests, that a bound they ask for is one.
func MemoryWatched() bool {
	return teamMemory.Load() != nil
}

// Haiku bounds the memory of no team: the RAM its areas keep is watched,
// and the program killed past the bound.
func startLimited(cmd *exec.Cmd, limits Limits, exceeded func()) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	used := teamMemory.Load()
	if used == nil {
		return nil
	}
	watch(cmd.Process.Pid, limits.Memory, *used, func() { _ = cmd.Process.Signal(syscall.SIGKILL) }, exceeded)
	return nil
}
