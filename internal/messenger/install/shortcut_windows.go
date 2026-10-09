// SPDX-License-Identifier: Unlicense OR MIT

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidShellLink  = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW   = windows.GUID{Data1: 0x000214f9, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIPersistFile = windows.GUID{Data1: 0x0000010b, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
)

// The methods of IShellLinkW and IPersistFile used, by their place in the
// interfaces' tables, after IUnknown's QueryInterface (0) and Release (2).
const (
	methodQueryInterface      = 0
	methodRelease             = 2
	methodSetDescription      = 7
	methodSetWorkingDirectory = 9
	methodSetIconLocation     = 17
	methodSetPath             = 20
	methodPersistSave         = 6
)

// comObject is what a COM interface pointer points to: the interface's
// table of methods.
type comObject struct{ table *[32]uintptr }

func (o *comObject) call(method int, args ...uintptr) error {
	hr, _, _ := syscall.SyscallN(o.table[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	if int32(hr) < 0 {
		return fmt.Errorf("HRESULT 0x%08x", uint32(hr))
	}
	return nil
}

func (o *comObject) release() { _ = o.call(methodRelease) }

// strings16 holds the UTF-16 strings handed to COM until the calls end.
type strings16 [][]uint16

func (s *strings16) ptr(v string) uintptr {
	u, _ := windows.UTF16FromString(v)
	*s = append(*s, u)
	return uintptr(unsafe.Pointer(&u[0]))
}

// makeShortcut saves a shortcut at path that starts exe in dir, with the
// program's icon, through the shell's own ShellLink object.
func makeShortcut(path, exe, dir string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const coinitApartmentThreaded = 0x2
	if hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded); int32(hr) < 0 {
		return fmt.Errorf("CoInitializeEx: HRESULT 0x%08x", uint32(hr))
	}
	defer procCoUninitialize.Call()
	const clsctxInprocServer = 0x1
	var link *comObject
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if int32(hr) < 0 {
		return fmt.Errorf("CoCreateInstance(ShellLink): HRESULT 0x%08x", uint32(hr))
	}
	defer link.release()
	var keep strings16
	defer runtime.KeepAlive(&keep)
	utf16 := keep.ptr
	for _, step := range []struct {
		method int
		args   []uintptr
	}{
		{methodSetPath, []uintptr{utf16(exe)}},
		{methodSetWorkingDirectory, []uintptr{utf16(dir)}},
		{methodSetDescription, []uintptr{utf16(appName)}},
		{methodSetIconLocation, []uintptr{utf16(exe), 0}},
	} {
		if err := link.call(step.method, step.args...); err != nil {
			return err
		}
	}
	var file *comObject
	if err := link.call(methodQueryInterface, uintptr(unsafe.Pointer(&iidIPersistFile)), uintptr(unsafe.Pointer(&file))); err != nil {
		return err
	}
	defer file.release()
	return file.call(methodPersistSave, utf16(path), 1)
}
