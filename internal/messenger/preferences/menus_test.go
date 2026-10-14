// SPDX-License-Identifier: Unlicense OR MIT
package preferences

import (
	"errors"
	"gioui.org/io/key"
	"komarugram/internal/messenger/shortcuts"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMenusPersistAcrossMissingContributions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	m := Menus{"message": {Order: []string{"account:1/count", "copy", "reply"}, Hidden: []string{"copy"}}}
	if err = s.SetMenus(m); err != nil {
		t.Fatal(err)
	}
	m["message"].Order[0] = "mutated caller"
	snapshot := s.Global()
	snapshot.Menus["message"].Hidden[0] = "mutated snapshot"
	restored, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	layout := restored.Global().Menus["message"]
	if !reflect.DeepEqual(s.Global().Menus, restored.Global().Menus) {
		t.Fatal("caller or snapshot mutated stored menus")
	}
	if got := layout.Arrange([]string{"reply", "copy", "delete"}, false); !reflect.DeepEqual(got, []string{"reply", "delete"}) {
		t.Fatalf("absent entry: %v", got)
	}
	layout = layout.WithOrder([]string{"delete", "copy", "reply"})
	if got := layout.Arrange([]string{"reply", "copy", "delete", "account:1/count", "new"}, false); !reflect.DeepEqual(got, []string{"account:1/count", "delete", "reply", "new"}) {
		t.Fatalf("returning entry lost its place: %v", got)
	}
	if err := restored.SetMenus(Menus{"message": layout}); err != nil {
		t.Fatal(err)
	}
}
func TestMenuOrderCannotCreateUnavailableActions(t *testing.T) {
	m := MenuLayout{Order: []string{"delete", "delete", "unknown", "copy"}, Hidden: []string{"copy"}}
	if got := m.Arrange([]string{"copy", "reply", "reply"}, false); !reflect.DeepEqual(got, []string{"reply"}) {
		t.Fatal(got)
	}
	if got := m.Arrange([]string{"copy", "reply"}, true); !reflect.DeepEqual(got, []string{"copy", "reply"}) {
		t.Fatal(got)
	}
}

func TestShortcutsPersistAndKeepSettingsAccessible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	b := shortcuts.Bindings{"app.settings": {{Key: "K", Modifiers: key.ModCtrl | key.ModAlt}}, "example.count": nil}
	if err = s.SetShortcuts(b); err != nil {
		t.Fatal(err)
	}
	b["app.settings"][0].Key = "Q"
	snapshot := s.Global()
	snapshot.Shortcuts["app.settings"][0].Key = "W"
	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Global().Shortcuts["app.settings"][0].Key != "K" || s.Global().Shortcuts["app.settings"][0].Key != "K" {
		t.Fatal("shortcut snapshot mutated")
	}
	if err = s.SetMenus(Menus{"sidebar": {Hidden: []string{"settings"}}, "drawer": {Hidden: []string{"settings"}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetShortcuts(shortcuts.Bindings{"app.settings": nil}); !errors.Is(err, ErrSettingsAccess) {
		t.Fatal("disabled final access to settings", err)
	}
	if len(s.Global().Shortcuts["app.settings"]) != 1 {
		t.Fatal("rejected save changed shortcuts")
	}
}
