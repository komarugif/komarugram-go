// SPDX-License-Identifier: Unlicense OR MIT

package appearance

import (
	"os/exec"
	"strings"
)

func startPlatform(m *Monitor) func() {
	return poll(m, readScheme, nil)
}

// readScheme asks for AppleInterfaceStyle, which is "Dark" in dark mode and
// not set at all in light mode.
func readScheme() Scheme {
	out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	if err != nil {
		return Light
	}
	if strings.TrimSpace(string(out)) == "Dark" {
		return Dark
	}
	return Light
}
