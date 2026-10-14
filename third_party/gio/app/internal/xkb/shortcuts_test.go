// SPDX-License-Identifier: Unlicense OR MIT
//go:build (linux && !android) || freebsd || openbsd

package xkb

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/io/key"
)

func TestNonLatinShortcuts(t *testing.T) {
	for _, layout := range []string{"us+ru:2+ua:3", "ru"} {
		t.Run(layout, func(t *testing.T) {
			x, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer x.Destroy()
			// Exercise the same keymap-loading and dispatch path as the window backend,
			// not manually constructed key.Event values that bypass XKB conversion.
			keymap := fmt.Sprintf(`xkb_keymap {
 xkb_keycodes { include "evdev+aliases(qwerty)" };
 xkb_types { include "complete" };
 xkb_compatibility { include "complete" };
 xkb_symbols { include "pc+%s" };
};`, layout) + "\x00"
			path := filepath.Join(t.TempDir(), "keymap")
			if err := os.WriteFile(path, []byte(keymap), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := x.LoadKeymap(1, int(f.Fd()), len(keymap)); err != nil {
				t.Fatal(err)
			}
			groups := 3
			if layout == "ru" {
				groups = 1
			}
			for group := 0; group < groups; group++ {
				for _, k := range []struct {
					code uint32
					name key.Name
				}{{38, "A"}, {54, "C"}} {
					x.UpdateMask(1<<2, 0, 0, 0, 0, uint32(group)) // Control, then selected layout.
					events := x.DispatchKey(k.code, key.Press)
					found := false
					for _, e := range events {
						if e, ok := e.(key.Event); ok {
							found = e.Name == k.name && e.Modifiers.Contain(key.ModCtrl)
						}
						if _, ok := e.(key.EditEvent); ok {
							t.Fatal("shortcut inserted text")
						}
					}
					if !found {
						t.Fatalf("group %d key %d: %+v", group, k.code, events)
					}
				}
			}
			group := uint32(1)
			if layout == "ru" {
				group = 0
			}
			x.UpdateMask(0, 0, 0, 0, 0, group)
			events := x.DispatchKey(54, key.Press)
			var name key.Name
			var text string
			for _, e := range events {
				switch e := e.(type) {
				case key.Event:
					name = e.Name
				case key.EditEvent:
					text = e.Text
				}
			}
			if name != "С" || text != "с" {
				t.Fatalf("ordinary Cyrillic input changed: name=%q text=%q", name, text)
			}
		})
	}
}

// With three layouts, each one types its own letters after a switch, as
// Wayland tells the layout in effect.
func TestWaylandLayoutGroups(t *testing.T) {
	x, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer x.Destroy()
	keymap := `xkb_keymap {
 xkb_keycodes { include "evdev+aliases(qwerty)" };
 xkb_types { include "complete" };
 xkb_compatibility { include "complete" };
 xkb_symbols { include "pc+ru+ua:2+us:3" };
};` + "\x00"
	path := filepath.Join(t.TempDir(), "keymap")
	if err := os.WriteFile(path, []byte(keymap), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := x.LoadKeymap(1, int(f.Fd()), len(keymap)); err != nil {
		t.Fatal(err)
	}
	// The S key: ы in Russian, і in Ukrainian, s in English.
	for group, want := range []string{"ы", "і", "s"} {
		x.UpdateModifiers(0, 0, 0, uint32(group))
		var text string
		for _, e := range x.DispatchKey(39, key.Press) {
			if e, ok := e.(key.EditEvent); ok {
				text = e.Text
			}
		}
		if text != want {
			t.Errorf("layout %d types %q, want %q", group, text, want)
		}
	}
}

func TestPhysicalPositionsSurviveLatinLayoutAndShift(t *testing.T) {
	for _, layout := range []string{"us", "us(dvorak)", "ru", "de"} {
		t.Run(layout, func(t *testing.T) {
			x, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer x.Destroy()
			keymap := fmt.Sprintf(`xkb_keymap { xkb_keycodes { include "evdev+aliases(qwerty)" }; xkb_types { include "complete" }; xkb_compatibility { include "complete" }; xkb_symbols { include "pc+%s" }; };`, layout) + "\x00"
			p := filepath.Join(t.TempDir(), "map")
			if err := os.WriteFile(p, []byte(keymap), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := x.LoadKeymap(1, int(f.Fd()), len(keymap)); err != nil {
				t.Fatal(err)
			}
			for _, mods := range []uint32{0, 1, 1 << 2, 1 << 3, (1 << 2) | 1} {
				x.UpdateMask(mods, 0, 0, 0, 0, 0)
				for _, k := range []struct {
					code     uint32
					position key.Name
				}{{24, "Q"}, {54, "C"}, {59, ","}, {21, "="}} {
					found := false
					for _, e := range x.DispatchKey(k.code, key.Press) {
						if event, ok := e.(key.Event); ok {
							found = event.Physical == k.position
						}
					}
					if !found {
						t.Fatalf("%s modifiers %d position %s lost", layout, mods, k.position)
					}
				}
			}
		})
	}
}
