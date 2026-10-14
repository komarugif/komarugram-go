//go:build linux && !android

// SPDX-License-Identifier: Unlicense OR MIT
package shortcuts

import (
	"context"
	"gioui.org/io/key"
	"github.com/godbus/dbus/v5"
	"strconv"
	"strings"
)

// Only query already-running services. In particular never activate
// kglobalacceld on another desktop, bind, steal or unregister a shortcut.
func CheckSystem(ctx context.Context, chords []Chord) SystemReport {
	out := SystemReport{Checked: map[Chord]bool{}, Occupied: map[Chord]bool{}}
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return out
	}
	defer conn.Close()
	has := func(name string) bool {
		var present bool
		_ = conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", dbus.FlagNoAutoStart, name).Store(&present)
		return present
	}
	if has("org.freedesktop.portal.Desktop") {
		var v dbus.Variant
		if conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop").CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", dbus.FlagNoAutoStart, "org.freedesktop.portal.GlobalShortcuts", "version").Store(&v) == nil {
			out.PortalVersion, _ = v.Value().(uint32)
		}
	}
	if !has("org.kde.kglobalaccel") {
		return out
	}
	out.KDE = true
	obj := conn.Object("org.kde.kglobalaccel", "/kglobalaccel")
	for _, chord := range chords {
		code, ok := QtKey(chord)
		if !ok {
			continue
		}
		var available bool
		if obj.CallWithContext(ctx, "org.kde.KGlobalAccel.isGlobalShortcutAvailable", dbus.FlagNoAutoStart, code, "").Store(&available) == nil {
			out.Checked[chord] = true
			out.Occupied[chord] = !available
		}
		if ctx.Err() != nil {
			break
		}
	}
	if len(out.Checked) == 0 {
		out.KDE = false
	}
	return out
}

// QtKey encodes a single QKeySequence stroke for KDE's advisory query. These
// logical US labels cannot enumerate all layout-dependent physical conflicts.
func QtKey(chord Chord) (int32, bool) {
	k := int32(0)
	if len(chord.Key) == 1 {
		k = int32(chord.Key[0])
	} else if strings.HasPrefix(string(chord.Key), "F") {
		n, err := strconv.Atoi(string(chord.Key[1:]))
		if err == nil && n >= 1 && n <= 24 {
			k = 0x01000030 + int32(n-1)
		}
	} else {
		k = map[key.Name]int32{key.NameLeftArrow: 0x01000012, key.NameUpArrow: 0x01000013, key.NameRightArrow: 0x01000014, key.NameDownArrow: 0x01000015, key.NameHome: 0x01000010, key.NameEnd: 0x01000011, key.NamePageUp: 0x01000016, key.NamePageDown: 0x01000017, key.NameSpace: 0x20}[chord.Key]
	}
	if k == 0 || chord.Modifiers&^AllModifiers != 0 {
		return 0, false
	}
	for _, v := range []struct {
		m  key.Modifiers
		qt int32
	}{{key.ModShift, 0x02000000}, {key.ModCtrl, 0x04000000}, {key.ModAlt, 0x08000000}, {key.ModSuper, 0x10000000}, {key.ModCommand, 0x10000000}} {
		if chord.Modifiers.Contain(v.m) {
			k |= v.qt
		}
	}
	return k, true
}
