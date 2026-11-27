// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows

package player

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"komarugram/pkg/program"
)

// findWindows locates VLC or mpv on Windows via registry keys (VideoLAN,
// Applications, Uninstall), standard install directories, and common package
// managers (WinGet, Scoop, Chocolatey).
func findWindows(k Kind) string {
	switch k {
	case VLC:
		return findWindowsVLC()
	case MPV:
		return findWindowsMPV()
	}
	return ""
}

func findWindowsVLC() string {
	// 1. Official VLC installer registry key (works for custom install dirs too).
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, view := range []uint32{0, registry.WOW64_32KEY} {
			k, err := registry.OpenKey(root, `SOFTWARE\VideoLAN\VLC`, registry.QUERY_VALUE|view)
			if err != nil {
				continue
			}
			exe, _, errExe := k.GetStringValue("")
			dir, _, errDir := k.GetStringValue("InstallDir")
			k.Close()
			if errExe == nil {
				if p := cleanWindowsExe(exe, "vlc.exe"); p != "" {
					return p
				}
			}
			if errDir == nil && dir != "" {
				if p := cleanWindowsExe(filepath.Join(dir, "vlc.exe"), "vlc.exe"); p != "" {
					return p
				}
			}
		}
	}
	// 2. Registered Applications shell open command.
	if p := findAppShellCommand("vlc.exe"); p != "" {
		return p
	}
	// 3. Uninstall entries.
	if p := findUninstallExe("vlc", "vlc.exe"); p != "" {
		return p
	}
	// 4. Standard directories and package managers.
	var candidates []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if dir := os.Getenv(env); dir != "" {
			candidates = append(candidates, filepath.Join(dir, "VideoLAN", "VLC", "vlc.exe"))
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		candidates = append(candidates,
			filepath.Join(local, "Programs", "VideoLAN", "VLC", "vlc.exe"),
			filepath.Join(local, "Microsoft", "WinGet", "Links", "vlc.exe"),
		)
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		candidates = append(candidates,
			filepath.Join(home, "scoop", "shims", "vlc.exe"),
			filepath.Join(home, "scoop", "apps", "vlc", "current", "vlc.exe"),
		)
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		candidates = append(candidates, filepath.Join(pd, "chocolatey", "bin", "vlc.exe"))
	}
	return firstExecutable(candidates)
}

func findWindowsMPV() string {
	// 1. Registered Applications shell open command (set by mpv-install.bat).
	if p := findAppShellCommand("mpv.exe"); p != "" {
		return p
	}
	// 2. Uninstall entries.
	if p := findUninstallExe("mpv", "mpv.exe"); p != "" {
		return p
	}
	// 3. Standard directories and package managers.
	var candidates []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if dir := os.Getenv(env); dir != "" {
			candidates = append(candidates, filepath.Join(dir, "mpv", "mpv.exe"))
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		candidates = append(candidates,
			filepath.Join(local, "Programs", "mpv", "mpv.exe"),
			filepath.Join(local, "mpv", "mpv.exe"),
			filepath.Join(local, "Microsoft", "WinGet", "Links", "mpv.exe"),
		)
		if matches, _ := filepath.Glob(filepath.Join(local, "Microsoft", "WinGet", "Packages", "*mpv*", "mpv.exe")); len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		candidates = append(candidates,
			filepath.Join(home, "scoop", "shims", "mpv.exe"),
			filepath.Join(home, "scoop", "apps", "mpv", "current", "mpv.exe"),
		)
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		candidates = append(candidates, filepath.Join(pd, "chocolatey", "bin", "mpv.exe"))
		if matches, _ := filepath.Glob(filepath.Join(pd, "chocolatey", "lib", "mpv*", "tools", "mpv.exe")); len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
	}
	candidates = append(candidates, `C:\mpv\mpv.exe`)
	return firstExecutable(candidates)
}

func firstExecutable(paths []string) string {
	for _, p := range paths {
		if filepath.IsAbs(p) && program.IsExecutable(p) {
			return p
		}
	}
	return ""
}

func findAppShellCommand(exeName string) string {
	subkey := `Software\Classes\Applications\` + exeName + `\shell\open\command`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{0, registry.WOW64_32KEY} {
			k, err := registry.OpenKey(root, subkey, registry.QUERY_VALUE|view)
			if err != nil {
				continue
			}
			cmd, _, err := k.GetStringValue("")
			k.Close()
			if err != nil {
				continue
			}
			if p := cleanWindowsExe(cmd, exeName); p != "" {
				return p
			}
		}
	}
	return ""
}

func findUninstallExe(nameSubstr, exeName string) string {
	const base = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, view := range []uint32{0, registry.WOW64_32KEY} {
			k, err := registry.OpenKey(root, base, registry.ENUMERATE_SUB_KEYS|view)
			if err != nil {
				continue
			}
			names, _ := k.ReadSubKeyNames(-1)
			k.Close()
			for _, sub := range names {
				sk, err := registry.OpenKey(root, base+`\`+sub, registry.QUERY_VALUE|view)
				if err != nil {
					continue
				}
				display, _, _ := sk.GetStringValue("DisplayName")
				loc, _, _ := sk.GetStringValue("InstallLocation")
				icon, _, _ := sk.GetStringValue("DisplayIcon")
				sk.Close()
				if !strings.Contains(strings.ToLower(display), nameSubstr) &&
					!strings.Contains(strings.ToLower(sub), nameSubstr) {
					continue
				}
				if loc != "" {
					if p := cleanWindowsExe(filepath.Join(strings.Trim(loc, `"`), exeName), exeName); p != "" {
						return p
					}
				}
				if icon != "" {
					if p := cleanWindowsExe(icon, exeName); p != "" {
						return p
					}
				}
			}
		}
	}
	return ""
}

// cleanWindowsExe extracts an executable path matching exeName from a Windows
// registry value (which may be quoted, have arguments, or end in ",0").
func cleanWindowsExe(raw, exeName string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	expanded, err := registry.ExpandString(raw)
	if err == nil && expanded != "" {
		raw = expanded
	}
	var path string
	if rest, ok := strings.CutPrefix(raw, `"`); ok {
		path, _, _ = strings.Cut(rest, `"`)
	} else {
		lower := strings.ToLower(raw)
		if idx := strings.Index(lower, ".exe"); idx >= 0 {
			path = raw[:idx+len(".exe")]
		} else {
			path = raw
		}
	}
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Base(path), exeName) {
		return ""
	}
	if program.IsExecutable(path) {
		return path
	}
	return ""
}
