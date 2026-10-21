// SPDX-License-Identifier: Unlicense OR MIT

//go:build darwin && cgo

package program

/*
#include <libproc.h>
#include <sys/resource.h>

// footprint is the physical footprint of process pid, what macOS counts
// against it, or -1 when it is gone.
static long long footprint(int pid) {
	struct rusage_info_v0 info;
	if (proc_pid_rusage(pid, RUSAGE_INFO_V0, (rusage_info_t *)&info) != 0) {
		return -1;
	}
	return (long long)info.ri_phys_footprint;
}
*/
import "C"

import (
	"os/exec"
	"syscall"
)

// macOS enforces no limit on the memory a process maps: its footprint is
// watched, and the program killed past the bound.
func startLimited(cmd *exec.Cmd, limits Limits, exceeded func()) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	watch(cmd.Process.Pid, limits.Memory, footprint, func() { _ = cmd.Process.Signal(syscall.SIGKILL) }, exceeded)
	return nil
}

func footprint(pid int) (uint64, bool) {
	n := C.footprint(C.int(pid))
	if n < 0 {
		return 0, false
	}
	return uint64(n), true
}
