// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// A program that is not on PATH is found where its installer registered it
// in the App Paths, as VLC's does.
func TestLookPathAppPaths(t *testing.T) {
	const name = "komarugram-apppath-test"
	exe := filepath.Join(t.TempDir(), name+".exe")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	key := `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\` + name + ".exe"
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.DeleteKey(registry.CURRENT_USER, key)
	err = k.SetStringValue("", `"`+exe+`"`)
	k.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := LookPath(name); err != nil || got != exe {
		t.Fatalf("LookPath: %q, %v; want %q", got, err, exe)
	}
	SetSearching(false)
	defer SetSearching(true)
	if _, err := LookPath(name); err == nil {
		t.Error("found with searching off")
	}
}
