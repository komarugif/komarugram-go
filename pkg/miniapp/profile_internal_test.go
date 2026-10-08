// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProfileResolve checks where each storage mode puts its profile and which
// of them is this launch's own.
func TestProfileResolve(t *testing.T) {
	root := t.TempDir()

	dir, ephemeral, err := Profile{Root: root}.resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if !ephemeral {
		t.Error("the zero profile is meant to be thrown away")
	}
	os.RemoveAll(dir)

	app, ephemeral, err := Profile{Storage: PerApp, Root: root, App: "kitchen_bot"}.resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if ephemeral {
		t.Error("a per-app profile is meant to be kept")
	}
	if want := filepath.Join(root, "apps", "kitchen_bot"); app != want {
		t.Errorf("per-app profile is %s, want %s", app, want)
	}
	if _, err := os.Stat(app); err != nil {
		t.Errorf("the directory was not created: %v", err)
	}

	// Two apps must not land in one directory, and an account must not land in
	// the directory of the app of the same name.
	other, _, err := Profile{Storage: PerApp, Root: root, App: "other_bot"}.resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if other == app {
		t.Error("two apps share a profile")
	}
	account, _, err := Profile{Storage: Shared, Root: root, Account: "kitchen_bot"}.resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if account == app {
		t.Error("an account shares the profile of a like-named app")
	}

	if _, _, err := (Profile{Storage: PerApp, Root: root}).resolve(""); err == nil {
		t.Error("a per-app profile without an app should be refused")
	}
}

// TestProfileKey checks that an identifier which is not a plain name cannot
// name a directory of its own choosing.
func TestProfileKey(t *testing.T) {
	for _, id := range []string{"..", ".", "../../etc", "a/b", "a b", "юзер"} {
		key := profileKey(id)
		if strings.ContainsAny(key, "/\\ ") || key == "." || key == ".." {
			t.Errorf("profileKey(%q) = %q, which is not a safe directory name", id, key)
		}
	}
	if profileKey("") != "default" {
		t.Error("an unnamed account should get a name of its own")
	}
	if profileKey("42") != "42" {
		t.Error("a plain id should be kept as it is")
	}
	if profileKey("a/b") == profileKey("a_b") {
		t.Error("two different ids collided")
	}
}

// TestSeedProfile checks that the preference is written into a fresh profile
// and that a profile the browser has already written is left alone.
func TestSeedProfile(t *testing.T) {
	dir := t.TempDir()
	if err := seedProfile(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Default", "Preferences")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"translate":{"enabled":false}`) {
		t.Errorf("a fresh profile was not seeded: %s", data)
	}
	// Brave keeps its notice at the browser level, not in the profile.
	state, err := os.ReadFile(filepath.Join(dir, "Local State"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"notice_acknowledged":true`) {
		t.Errorf("the browser-level settings were not seeded: %s", state)
	}

	written := `{"translate":{"enabled":false},"browser":{"window_placement":{}}}`
	if err := os.WriteFile(path, []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}
	kept := `{"brave":{"p3a":{"enabled":true}}}`
	if err := os.WriteFile(filepath.Join(dir, "Local State"), []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedProfile(dir); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "Local State")); err != nil {
		t.Fatal(err)
	} else if string(data) != kept {
		t.Errorf("the browser's own settings were overwritten: %s", data)
	}
	if data, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if string(data) != written {
		t.Errorf("the browser's own preferences were overwritten: %s", data)
	}
}
