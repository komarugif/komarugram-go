// SPDX-License-Identifier: Unlicense OR MIT

package deviceinfo

import (
	"bufio"
	"strings"
	"sync"
)

// Platform is the operating system for the user to read, with its kernel:
// "Ubuntu 24.04.1 LTS (Linux 6.8.0-45-generic)", "Windows 7 Ultimate
// Service Pack 1 (NT 6.1.7601)", "macOS 15.1 (Darwin 24.1.0)". It is not
// the name Telegram is told (System).
func Platform() string { return platformOnce() }

var platformOnce = sync.OnceValue(func() string {
	name, kernel := platform()
	return platformText(name, kernel)
})

// platformText is name with kernel in parentheses, or what of them there is.
func platformText(name, kernel string) string {
	name, kernel = strings.TrimSpace(name), strings.TrimSpace(kernel)
	switch {
	case name == "":
		return kernel
	case kernel == "":
		return name
	}
	// The kernel's name and version stay on one line.
	return name + " (" + strings.ReplaceAll(kernel, " ", "\u00a0") + ")"
}

// osReleaseName is the name os-release gives the distribution:
// PRETTY_NAME, else NAME with VERSION.
func osReleaseName(data string) string {
	values := map[string]string{}
	s := bufio.NewScanner(strings.NewReader(data))
	for s.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(s.Text()), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		values[key] = strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\$`, `$`, "\\`", "`").Replace(value)
	}
	if name := values["PRETTY_NAME"]; name != "" {
		return name
	}
	return strings.TrimSpace(values["NAME"] + " " + values["VERSION"])
}

// windowsName is the name of Windows from its registry: the product with
// its service pack. Windows 11 keeps calling itself Windows 10 there; its
// builds from 22000 tell it.
func windowsName(product, servicePack string, build uint32) string {
	if build >= 22000 {
		product = strings.Replace(product, "Windows 10", "Windows 11", 1)
	}
	if product == "" {
		product = "Windows"
	}
	return strings.TrimSpace(product + " " + servicePack)
}
