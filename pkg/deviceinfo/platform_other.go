// SPDX-License-Identifier: Unlicense OR MIT

//go:build !linux && !windows && !darwin && !haiku && !freebsd && !openbsd && !netbsd && !dragonfly

package deviceinfo

import "runtime"

func platform() (string, string) { return runtime.GOOS, "" }
