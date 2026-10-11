// SPDX-License-Identifier: Unlicense OR MIT

package deviceinfo

import (
	"context"
	"komarugram/pkg/program"
	"os"
	"path/filepath"
	"time"
)

// model reads what the firmware says of the computer from sysfs, then asks
// systemd whether it is a virtual machine, then goes by the chassis.
func model() string {
	value := func(name string) string {
		b, err := os.ReadFile("/sys/class/dmi/id/" + name)
		if err != nil {
			return ""
		}
		return simplifyModel(string(b))
	}
	if m := firmwareModel(value("product_name"), value("product_family"), value("board_name")); m != "" {
		return m
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// It exits with 1 when it prints "none".
	out, _ := program.CommandContext(ctx, "systemd-detect-virt").Output()
	if m := virtualizationModel(string(out)); m != "" {
		return m
	}
	return chassisModel(value("chassis_type"))
}

func system() string {
	libc, version := libcVersion()
	return linuxSystem(desktopEnvironments(os.Getenv("XDG_CURRENT_DESKTOP")), displayServer(), libc, version)
}

// waylandBuilt and x11Built tell whether Gio can open windows there: the
// nowayland and nox11 build tags leave them out.
var waylandBuilt, x11Built bool

// displayServer names what Gio shows windows through: Wayland when a
// compositor is there, then X11 — Xwayland when a Wayland compositor runs it.
func displayServer() string {
	wayland := waylandSocket()
	switch {
	case waylandBuilt && wayland:
		return "Wayland"
	case x11Built && os.Getenv("DISPLAY") != "" && wayland:
		return "Xwayland"
	case x11Built && os.Getenv("DISPLAY") != "":
		return "X11"
	}
	return ""
}

// waylandSocket reports whether there is the socket that
// wl_display_connect(NULL) connects to.
func waylandSocket() bool {
	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	if !filepath.IsAbs(name) {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			return false
		}
		name = filepath.Join(dir, name)
	}
	fi, err := os.Stat(name)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}
