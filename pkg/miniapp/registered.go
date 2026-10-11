// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import "strings"

// registeredNames are the program files of the browsers taken from those
// Windows lists as installed (registeredBrowsers): built on Chromium or on
// Firefox. The list holds others, Internet Explorer among them, whose
// version resource reads like Chromium's.
var registeredNames = map[string]bool{
	"chrome.exe":    true, // Chrome, Chromium, Supermium
	"msedge.exe":    true,
	"brave.exe":     true,
	"firefox.exe":   true,
	"librewolf.exe": true,
	"waterfox.exe":  true,
}

// commandProgram returns the program of a command line as Windows keeps it
// for a registered browser, `"C:\Program Files\Supermium\chrome.exe" --flag`
// or unquoted, if it is one of registeredNames.
func commandProgram(command string) (string, bool) {
	command = strings.TrimSpace(command)
	var path string
	if rest, ok := strings.CutPrefix(command, `"`); ok {
		path, _, ok = strings.Cut(rest, `"`)
		if !ok {
			return "", false
		}
	} else {
		i := strings.Index(strings.ToLower(command), ".exe")
		if i < 0 {
			return "", false
		}
		path = command[:i+len(".exe")]
	}
	base := strings.ToLower(path[strings.LastIndexAny(path, `\/`)+1:])
	// A program not given by its full path would be looked for in the
	// working directory.
	absolute := len(path) > 2 && path[1] == ':' || strings.HasPrefix(path, `\\`)
	if !registeredNames[base] || !absolute {
		return "", false
	}
	return path, true
}
