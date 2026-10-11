// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// appPath looks name up where Windows itself finds a program by name, as
// Run and ShellExecute do: the App Paths of the user and of the machine,
// where installers such as VLC's register a program they do not put on
// PATH.
func appPath(name string) (string, bool) {
	if filepath.Ext(name) == "" {
		name += ".exe"
	}
	if strings.ContainsAny(name, `\/`) {
		return "", false
	}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, _, err := k.GetStringValue("")
		k.Close()
		if err != nil {
			continue
		}
		path, _ = registry.ExpandString(strings.Trim(path, `"`))
		if info, err := os.Stat(path); err == nil && !info.IsDir() && filepath.IsAbs(path) {
			return path, true
		}
	}
	return "", false
}
