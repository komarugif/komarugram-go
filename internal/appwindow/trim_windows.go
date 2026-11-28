// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows

package appwindow

import "golang.org/x/sys/windows"

var procSetProcessWorkingSetSize = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessWorkingSetSize")

// trimCHeap asks Windows to trim unused pages from the process working set
// (the Windows equivalent of glibc's malloc_trim(0) in trim_glibc.go).
func trimCHeap() {
	if procSetProcessWorkingSetSize.Find() != nil {
		return
	}
	const trim = ^uintptr(0) // SIZE_T(-1) trims the working set
	_, _, _ = procSetProcessWorkingSetSize.Call(uintptr(windows.CurrentProcess()), trim, trim)
}
