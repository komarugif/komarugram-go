// SPDX-License-Identifier: Unlicense OR MIT

package player

import "komarugram/pkg/miniapp"

// The tests open the browser headless and draw nothing, so they run without
// the GPU: on a system whose driver hangs Chromium's windows, as
// VirtualBox's does for Supermium on Windows 7, they would time out otherwise.
func init() { miniapp.GPUFailed = func() bool { return true } }
