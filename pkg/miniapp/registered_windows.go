// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// registeredBrowsers lists the browsers installed on Windows, which are
// rarely on PATH: each registers under Clients\StartMenuInternet, for the
// user or for the machine, with the command that opens it.
func registeredBrowsers() []string {
	var found []string
	seen := map[string]bool{}
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
				if path, ok := commandProgram(command); ok && !seen[strings.ToLower(path)] {
					seen[strings.ToLower(path)] = true
					found = append(found, path)
				}
			}
		}
	}
	return found
}
