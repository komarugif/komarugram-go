// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// registeredBrowsers lists the browsers installed on Windows, which are
// rarely on PATH: each registers under Clients\StartMenuInternet, for the
// user or for the machine, with the command that opens it, plus standard
// install paths for Edge and Zen if absent from StartMenuInternet.
func registeredBrowsers() []string {
	var found []string
	seen := map[string]bool{}
	add := func(path string) {
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		seen[key] = true
		found = append(found, path)
	}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{0, registry.WOW64_32KEY} {
			k, err := registry.OpenKey(root, `SOFTWARE\Clients\StartMenuInternet`, registry.ENUMERATE_SUB_KEYS|view)
			if err != nil {
				continue
			}
			names, _ := k.ReadSubKeyNames(-1)
			k.Close()
			for _, name := range names {
				c, err := registry.OpenKey(root, `SOFTWARE\Clients\StartMenuInternet\`+name+`\shell\open\command`, registry.QUERY_VALUE|view)
				if err != nil {
					continue
				}
				command, _, err := c.GetStringValue("")
				c.Close()
				if err != nil {
					continue
				}
				if path, ok := commandProgram(command); ok {
					add(path)
				}
			}
		}
	}
	for _, fallback := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Zen Browser", "zen.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Zen Browser", "zen.exe"),
	} {
		if filepath.IsAbs(fallback) {
			if st, err := os.Stat(fallback); err == nil && !st.IsDir() {
				add(fallback)
			}
		}
	}
	return found
}
