// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"fmt"
	"log"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Messages a job object's completion port gets (winnt.h).
const (
	jobMsgActiveProcessZero  = 8
	jobMsgProcessMemoryLimit = 9
)

func startLimited(cmd *exec.Cmd, limits Limits, exceeded func()) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	// Suspended until it is in the job: it allocates nothing unbounded.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := uint32(cmd.Process.Pid)
	if err := jobLimit(pid, limits, exceeded); err != nil {
		// In a job that takes no other, as a client started by some
		// launchers on Windows 7 is: its memory is watched instead.
		log.Printf("program: memory of %s watched: %v", cmd.Path, err)
		watch(int(pid), limits.Memory, committed, func() { _ = cmd.Process.Kill() }, exceeded)
	}
	if err := resume(pid); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("resume %s: %w", cmd.Path, err)
	}
	return nil
}

// jobLimit puts process pid in a job of its own that bounds the memory it
// commits, and kills it, and calls exceeded, once it asks for more. The
// job goes once the process has ended.
func jobLimit(pid uint32, limits Limits, exceeded func()) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
		windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	info.ProcessMemoryLimit = uintptr(limits.Memory)
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 1)
	if err != nil {
		windows.CloseHandle(job)
		return err
	}
	// JOBOBJECT_ASSOCIATE_COMPLETION_PORT.
	assoc := struct{ CompletionKey, CompletionPort windows.Handle }{job, port}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectAssociateCompletionPortInformation,
		uintptr(unsafe.Pointer(&assoc)), uint32(unsafe.Sizeof(assoc))); err != nil {
		windows.CloseHandle(port)
		windows.CloseHandle(job)
		return err
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(port)
		windows.CloseHandle(job)
		return err
	}
	go func() {
		defer windows.CloseHandle(port)
		// Closed, the job kills what is left in it: nothing, by then.
		defer windows.CloseHandle(job)
		for {
			var msg uint32
			var key uintptr
			var overlapped *windows.Overlapped
			if err := windows.GetQueuedCompletionStatus(port, &msg, &key, &overlapped, windows.INFINITE); err != nil {
				return
			}
			switch msg {
			case jobMsgProcessMemoryLimit:
				// The allocation failed: the program may go on without
				// it, or crash; it is killed either way.
				_ = windows.TerminateJobObject(job, 1)
				exceeded()
			case jobMsgActiveProcessZero:
				return
			}
		}
	}()
	return nil
}

// resume resumes the first thread of process pid, which it was started
// suspended with: the only one it has then.
func resume(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return fmt.Errorf("no thread of process %d", pid)
}

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS_EX.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
	privateUsage               uintptr
}

// committed is the memory process pid committed, as a job counts it, and
// whether it still runs.
func committed(pid int) (uint64, bool) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(process)
	if event, _ := windows.WaitForSingleObject(process, 0); event == windows.WAIT_OBJECT_0 {
		return 0, false
	}
	var c processMemoryCounters
	c.cb = uint32(unsafe.Sizeof(c))
	if ok, _, _ := procGetProcessMemoryInfo.Call(uintptr(process), uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); ok == 0 {
		return 0, true
	}
	return uint64(c.privateUsage), true
}
