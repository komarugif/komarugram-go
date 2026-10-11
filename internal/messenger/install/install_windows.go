// SPDX-License-Identifier: Unlicense OR MIT

package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"komarugram/pkg/program"
)

const supported = true

const exeName = appName + ".exe"

// uninstallKey is the program's key in the list of installed programs,
// in HKEY_CURRENT_USER, or HKEY_LOCAL_MACHINE for a copy of every user.
// Tests use a key of their own.
var uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + id

// madeValue is the key's own value telling that the installation made its
// folder, 1 if it did.
const madeValue = "KomaruGramMadeFolder"

// userDir is %LOCALAPPDATA%\Programs\KomaruGram, where the installers that
// need no administrator put programs of one user.
func userDir() string {
	base, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		base = os.Getenv("LOCALAPPDATA")
	}
	return filepath.Join(base, "Programs", appName)
}

func knownFolder(folder *windows.KNOWNFOLDERID) (string, error) {
	return windows.KnownFolderPath(folder, 0)
}

// shortcutPaths are the program's shortcut on the desktop and in the
// Start menu, of this user.
func shortcutPaths() (desktop, menu string, err error) {
	d, err := knownFolder(windows.FOLDERID_Desktop)
	if err != nil {
		return "", "", err
	}
	m, err := knownFolder(windows.FOLDERID_Programs)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(d, appName+".lnk"), filepath.Join(m, listName+".lnk"), nil
}

func install(ctx context.Context, self, dir string, o Options) (string, error) {
	exe := filepath.Join(dir, exeName)
	existing, installed := findIn(registry.CURRENT_USER)
	system := false
	switch err := writable(dir); {
	case errors.Is(err, ErrNeedsAdmin):
		// The copy and its registration for every user are made by the
		// program itself, started with the administrator's rights; the
		// shortcuts below stay the user's own.
		if err := elevate(self, "-"+FlagInstallSystem, dir); err != nil {
			return "", err
		}
		system = true
	case err != nil:
		return "", err
	default:
		made := madeFolder(dir, existing, installed)
		if err := copyProgram(self, exe); err != nil {
			return "", err
		}
		if err := register(registry.CURRENT_USER, exe, made); err != nil {
			return "", err
		}
	}
	// A copy registered once for the user and once for the machine would
	// show twice in the list.
	if system {
		_ = registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
	}
	desktop, menu, err := shortcutPaths()
	if err != nil {
		return "", err
	}
	for _, s := range []struct {
		path string
		want bool
	}{{desktop, o.Desktop}, {menu, o.Menu}} {
		if s.want {
			if err := makeShortcut(s.path, exe, dir); err != nil {
				return "", fmt.Errorf("install: shortcut %s: %w", s.path, err)
			}
		} else if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return exe, nil
}

// InstallSystem copies the program to dir and registers it for every
// user. It is what the program does when started with -install-system,
// with the administrator's rights.
func InstallSystem(dir string) error {
	dir, err := cleanDir(dir)
	if err != nil {
		return err
	}
	self, err := executable()
	if err != nil {
		return err
	}
	exe := filepath.Join(dir, exeName)
	existing, installed := findIn(registry.LOCAL_MACHINE)
	made := madeFolder(dir, existing, installed)
	if err := copyProgram(self, exe); err != nil {
		return err
	}
	return register(registry.LOCAL_MACHINE, exe, made)
}

// UninstallSystem removes the copy in dir registered for every user: what
// the program does when started with -uninstall-system.
func UninstallSystem(dir string) error {
	dir, err := cleanDir(dir)
	if err != nil {
		return err
	}
	// What is removed, and whether the folder goes, is the registration's
	// word, not the command line's.
	inst, ok := findIn(registry.LOCAL_MACHINE)
	if !ok || !sameFolderPath(inst.Dir, dir) {
		return fmt.Errorf("install: no copy for every user is registered in %s", dir)
	}
	if err := removeProgram(inst); err != nil {
		return err
	}
	if err := registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// register writes the program's key in the list of installed programs
// of root: its name, icon, folder, size and the command that removes it,
// and whether the installation made the folder.
func register(root registry.Key, exe string, made bool) error {
	k, _, err := registry.CreateKey(root, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	size := uint32(0)
	if info, err := os.Stat(exe); err == nil {
		size = uint32(info.Size() / 1024)
	}
	for name, value := range map[string]string{
		"DisplayName":     listName,
		"DisplayIcon":     exe + ",0",
		"InstallLocation": filepath.Dir(exe),
		"UninstallString": windows.EscapeArg(exe) + " -uninstall",
		"Publisher":       appName,
	} {
		if err := k.SetStringValue(name, value); err != nil {
			return err
		}
	}
	madeFlag := uint32(0)
	if made {
		madeFlag = 1
	}
	for name, value := range map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": size, madeValue: madeFlag} {
		if err := k.SetDWordValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

// Find is the installed copy, from its key in the list of installed
// programs: the user's own, or else the machine's.
func Find() (Installation, bool) {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		if inst, ok := findIn(root); ok {
			return inst, true
		}
	}
	return Installation{}, false
}

// findIn is the copy registered in root.
func findIn(root registry.Key) (Installation, bool) {
	k, err := registry.OpenKey(root, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return Installation{}, false
	}
	defer k.Close()
	dir, _, err := k.GetStringValue("InstallLocation")
	if err != nil || !filepath.IsAbs(dir) {
		return Installation{}, false
	}
	made, _, _ := k.GetIntegerValue(madeValue)
	return Installation{Dir: dir, Exe: filepath.Join(dir, exeName), System: root == registry.LOCAL_MACHINE, MadeFolder: made == 1}, true
}

func uninstall(ctx context.Context, inst Installation) error {
	self, _ := executable()
	if sameFile(self, inst.Exe) {
		// A running program cannot be removed on Windows: Relocate makes
		// a copy elsewhere do it.
		return errors.New("install: the installed copy cannot remove itself while it runs")
	}
	if inst.System {
		if err := elevate(self, "-"+FlagUninstallSystem, inst.Dir); err != nil {
			return err
		}
	} else {
		if err := removeProgram(inst); err != nil {
			if errors.Is(err, os.ErrPermission) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
				return fmt.Errorf("%w: %v", ErrInUse, err)
			}
			return err
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, uninstallKey); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	desktop, menu, err := shortcutPaths()
	if err != nil {
		return err
	}
	for _, path := range []string{desktop, menu} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Relocate makes the removal run from a copy of the program outside its
// folder, which the copy can then remove: it copies the program to the
// temporary folder and starts the copy with args. It reports false when
// this process is not the installed copy, and should do the removal
// itself.
func Relocate(args ...string) (bool, error) {
	inst, ok := Find()
	self, err := executable()
	if err != nil || !ok || !sameFile(self, inst.Exe) {
		return false, err
	}
	tmp, err := os.MkdirTemp("", id+"-uninstall-")
	if err != nil {
		return true, err
	}
	copy := filepath.Join(tmp, exeName)
	if err := copyProgram(self, copy); err != nil {
		return true, err
	}
	// The copy works from its own folder: a folder that is a process's
	// current one cannot be removed on Windows, and the copy inherits this
	// one's, which the list of programs may set to the installation's.
	cmd := program.Command(copy, args...)
	cmd.Dir = tmp
	return true, cmd.Start()
}

// RemoveLater removes this program, a copy Relocate made, a few seconds
// after it ends: cmd waits, then deletes the file and its folder.
func RemoveLater() error {
	self, err := executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(self)
	if !strings.HasPrefix(filepath.Base(dir), id+"-uninstall-") {
		return nil
	}
	script := fmt.Sprintf(`ping -n 4 127.0.0.1 >nul & del /f /q "%s" & rmdir "%s"`, self, dir)
	cmd := program.Command("cmd.exe")
	cmd.Dir = os.TempDir()
	cmd.SysProcAttr.CmdLine = `cmd.exe /d /c "` + script + `"`
	return cmd.Start()
}

var (
	shell32            = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteEx = shell32.NewProc("ShellExecuteExW")
)

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	size          uint32
	mask          uint32
	hwnd          windows.Handle
	verb          *uint16
	file          *uint16
	parameters    *uint16
	directory     *uint16
	show          int32
	instApp       windows.Handle
	idList        uintptr
	class         *uint16
	keyClass      windows.Handle
	hotKey        uint32
	iconOrMonitor windows.Handle
	process       windows.Handle
}

const seeMaskNoCloseProcess = 0x40

// elevate runs exe with args and the administrator's rights, which the
// system asks the user for, and waits for it to end.
func elevate(exe string, args ...string) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = windows.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return err
	}
	// The elevated program works from the temporary folder, not from
	// a folder it may be about to remove.
	directory, err := windows.UTF16PtrFromString(os.TempDir())
	if err != nil {
		return err
	}
	info := shellExecuteInfo{mask: seeMaskNoCloseProcess, verb: verb, file: file, parameters: params, directory: directory, show: windows.SW_HIDE}
	info.size = uint32(unsafe.Sizeof(info))
	if ok, _, err := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return ErrCancelled
		}
		return fmt.Errorf("install: start with the administrator's rights: %w", err)
	}
	if info.process == 0 {
		return errors.New("install: the program with the administrator's rights did not start")
	}
	defer windows.CloseHandle(info.process)
	if _, err := windows.WaitForSingleObject(info.process, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("install: the program with the administrator's rights failed (exit code %d)", code)
	}
	return nil
}
