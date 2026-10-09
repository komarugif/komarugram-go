// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package miniapp

// registeredBrowsers lists the browsers a system keeps a list of: only
// Windows does.
func registeredBrowsers() []string { return nil }
