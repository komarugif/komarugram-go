// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku && cgo

package program

/*
#include <OS.h>

// teamMemory is the memory the areas of team keep in RAM, or -1 when it
// is gone.
static long long teamMemory(int team) {
	area_info info;
	ssize_t cookie = 0;
	long long sum = 0;
	team_info about;
	if (get_team_info(team, &about) != B_OK) {
		return -1;
	}
	while (get_next_area_info(team, &cookie, &info) == B_OK) {
		sum += info.ram_size;
	}
	return sum;
}
*/
import "C"

import (
	"os/exec"
	"syscall"
)

// Haiku bounds the memory of no team: the RAM its areas keep is watched,
// and the program killed past the bound.
func startLimited(cmd *exec.Cmd, limits Limits, exceeded func()) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	watch(cmd.Process.Pid, limits.Memory, teamMemory, func() { _ = cmd.Process.Signal(syscall.SIGKILL) }, exceeded)
	return nil
}

func teamMemory(pid int) (uint64, bool) {
	n := C.teamMemory(C.int(pid))
	if n < 0 {
		return 0, false
	}
	return uint64(n), true
}
