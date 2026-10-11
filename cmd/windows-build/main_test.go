// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// gogio must not see or overwrite .syso files left by previous builds.
// Staging must also retain the repository's local Gio replacement.
func TestPrepareWorkIsolatesResourcesAndKeepsFork(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repository with spaces")
	src := filepath.Join(repo, "cmd", "messenger")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                     "module komarugram\n\ngo 1.27.1\n\nreplace gioui.org => ./third_party/gio\n",
		"go.sum":                     "",
		"cmd/messenger/main.go":      "package main\nfunc main() {}\n",
		"cmd/messenger/main_test.go": "package main\n",
		"cmd/messenger/previous_windows_amd64.syso": "existing resource",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(name)), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	work := t.TempDir()
	env := setEnv(os.Environ(), "GOWORK", "off")
	if err := prepareWork(repo, work, env); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		got, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(name)))
		if err != nil || string(got) != data {
			t.Fatalf("changed original %s: %q, %v", name, got, err)
		}
	}
	for _, name := range []string{"previous_windows_amd64.syso", "main_test.go"} {
		if _, err := os.Stat(filepath.Join(work, "messenger", name)); !os.IsNotExist(err) {
			t.Fatalf("staged unwanted file %s: %v", name, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(work, "messenger", "main.go"))
	if err != nil || string(got) != files["cmd/messenger/main.go"] {
		t.Fatalf("staged main.go = %q, %v", got, err)
	}
	text, err := goOutput(work, env, "mod", "edit", "-json")
	if err != nil {
		t.Fatal(err)
	}
	var mod struct {
		Module  struct{ Path string }
		Replace []struct{ Old, New struct{ Path string } }
	}
	if err := json.Unmarshal([]byte(text), &mod); err != nil {
		t.Fatal(err)
	}
	if mod.Module.Path != "komarugram/windowsbuild" {
		t.Fatalf("staged module = %q", mod.Module.Path)
	}
	found := false
	for _, r := range mod.Replace {
		if r.Old.Path == "gioui.org" {
			found = filepath.Clean(r.New.Path) == filepath.Join(repo, "third_party", "gio")
		}
	}
	if !found {
		t.Fatal("staged module lost the local Gio fork")
	}
}
