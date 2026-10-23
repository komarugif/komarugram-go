// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"

	"golang.org/x/sys/unix"
)

func startLimited(cmd *exec.Cmd, limits Limits, exceeded func()) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	// RLIMIT_DATA counts the heap, anonymous mappings and the stacks of
	// threads, not mapped files: an allocation past it fails. RLIMIT_AS
	// would count the address space a decoder or a driver reserves and
	// never uses. Set right after the start, it leaves the dynamic loader
	// unbounded, which reads no file of the user's.
	limit := unix.Rlimit{Cur: limits.Memory, Max: limits.Memory}
	_ = unix.Prlimit(pid, unix.RLIMIT_DATA, &limit, nil)
	// What RLIMIT_DATA leaves out, or a system that refused it, is caught
	// by watching what the program keeps in memory.
	watch(pid, limits.Memory, resident, func() { _ = cmd.Process.Kill() }, exceeded)
	return nil
}

// resident is what process pid keeps in memory (VmRSS), and whether it
// still runs.
func resident(pid int) (uint64, bool) {
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0, false
	}
	if i := bytes.Index(status, []byte("\nState:\tZ")); i >= 0 {
		// A zombie: it ended, and waits for its parent.
		return 0, false
	}
	i := bytes.Index(status, []byte("\nVmRSS:"))
	if i < 0 {
		return 0, true
	}
	line := status[i+len("\nVmRSS:"):]
	if end := bytes.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	fields := bytes.Fields(line)
	if len(fields) == 0 {
		return 0, true
	}
	kb, err := strconv.ParseUint(string(fields[0]), 10, 64)
	if err != nil {
		return 0, true
	}
	return kb << 10, true
}
