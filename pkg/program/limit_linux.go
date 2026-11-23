// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"

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
	limit := unix.Rlimit{Cur: limits.Memory + limits.Reserved, Max: limits.Memory + limits.Reserved}
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
	return statusKB(status, "\nVmRSS:") << 10, true
}

// reserved runs the program at path with args and returns the data it
// took (VmData, what RLIMIT_DATA counts) once it sleeps and the figure
// stays the same a few looks on: it waits for its standard input, which is
// kept open and empty, so that it reads nothing. It is 0 when the program
// ended or did not come to wait in time.
func reserved(path string, args []string) uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), reservedTimeout)
	defer cancel()
	cmd := CommandContext(ctx, path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		return 0
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	var last uint64
	same := 0
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-tick.C:
		}
		data, sleeping, alive := dataOf(pid)
		if !alive {
			return 0
		}
		if !sleeping || data != last {
			last, same = data, 0
			continue
		}
		// Asleep with the same data for 20 ms: waiting for its input.
		if same++; same == 4 {
			return data
		}
	}
}

// dataOf is the data process pid took (VmData), whether it sleeps, and
// whether it still runs.
func dataOf(pid int) (data uint64, sleeping, alive bool) {
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil || bytes.Contains(status, []byte("\nState:\tZ")) {
		return 0, false, false
	}
	sleeping = bytes.Contains(status, []byte("\nState:\tS"))
	return statusKB(status, "\nVmData:") << 10, sleeping, true
}

// statusKB reads a figure in kB of /proc/<pid>/status, after field.
func statusKB(status []byte, field string) uint64 {
	i := bytes.Index(status, []byte(field))
	if i < 0 {
		return 0
	}
	line := status[i+len(field):]
	if end := bytes.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	fields := bytes.Fields(line)
	if len(fields) == 0 {
		return 0
	}
	kb, err := strconv.ParseUint(string(fields[0]), 10, 64)
	if err != nil {
		return 0
	}
	return kb
}
