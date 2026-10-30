// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

// The tests open browsers headless and draw nothing, so they run without the
// GPU: on a system whose driver hangs Chromium's windows, as VirtualBox's
// does for Supermium on Windows 7, they would time out otherwise.
func init() { GPUFailed = func() bool { return true } }
