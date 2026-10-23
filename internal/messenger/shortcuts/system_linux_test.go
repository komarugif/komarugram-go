//go:build linux && !android

// SPDX-License-Identifier: Unlicense OR MIT
package shortcuts

import (
	"bufio"
	"context"
	"gioui.org/io/key"
	"github.com/godbus/dbus/v5"
	"os/exec"
	"testing"
	"time"
)

func TestQtShortcutEncoding(t *testing.T) {
	for _, v := range []struct {
		chord Chord
		want  int32
	}{{Chord{Key: "T", Modifiers: key.ModCtrl | key.ModAlt}, 0x0c000054}, {Chord{Key: key.NameF1, Modifiers: key.ModSuper}, 0x11000030}, {Chord{Key: key.NameLeftArrow, Modifiers: key.ModAlt}, 0x09000012}} {
		got, ok := QtKey(v.chord)
		if !ok || got != v.want {
			t.Fatal(v.chord, got, v.want)
		}
	}
	if _, ok := QtKey(Chord{Key: "IntlBackslash"}); ok {
		t.Fatal("unmapped physical key falsely queried")
	}
}

func TestKDEConflictProbeDoesNotActivateMissingService(t *testing.T) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	cmd := exec.Command(daemon, "--session", "--nofork", "--print-address=1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(pipe)
	if !scanner.Scan() {
		t.Fatal("private bus did not provide an address")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", scanner.Text())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	occupied := Chord{Key: "T", Modifiers: key.ModCtrl | key.ModAlt}
	if got := CheckSystem(ctx, []Chord{occupied}); got.KDE || len(got.Checked) != 0 {
		t.Fatal("absent service reported as checked", got)
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	code, _ := QtKey(occupied)
	err = conn.ExportMethodTable(map[string]interface{}{"isGlobalShortcutAvailable": func(candidate int32, component string) (bool, *dbus.Error) {
		if component != "" {
			return false, dbus.NewError("org.example.Invalid", nil)
		}
		return candidate != code, nil
	}}, "/kglobalaccel", "org.kde.KGlobalAccel")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.RequestName("org.kde.kglobalaccel", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	free := Chord{Key: "K", Modifiers: key.ModCtrl | key.ModAlt}
	got := CheckSystem(ctx, []Chord{occupied, free})
	if !got.KDE || !got.Checked[occupied] || !got.Occupied[occupied] || !got.Checked[free] || got.Occupied[free] {
		t.Fatal("KDE occupancy not projected correctly", got)
	}
}
