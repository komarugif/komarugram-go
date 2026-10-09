// SPDX-License-Identifier: Unlicense OR MIT

package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderFor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Programs")
	for dir, want := range map[string]string{
		root:                                 root + string(filepath.Separator) + appName,
		filepath.Join(root, appName):         filepath.Join(root, appName),
		filepath.Join(root, "komarugram"):    filepath.Join(root, "komarugram"),
		filepath.Join(root, "Telegram"):      filepath.Join(root, "Telegram", appName),
		filepath.Join(root, "KOMARUGRAM-GO"): filepath.Join(root, "KOMARUGRAM-GO"),
		"":                                   "",
	} {
		if got := FolderFor(dir); got != want {
			t.Errorf("FolderFor(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestCleanDirRefusesRootsAndRelativePaths(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	for _, dir := range []string{"", "relative", root} {
		if _, err := cleanDir(dir); err == nil {
			t.Errorf("cleanDir(%q) accepted it", dir)
		}
	}
}

// A build of `go run` is not offered to be installed.
func TestGoRunIsManaged(t *testing.T) {
	if !managed(filepath.Join(t.TempDir(), "go-build123", "b001", "exe", "messenger")) {
		t.Error("a go run build is offered")
	}
	if managed(filepath.Join(t.TempDir(), "Downloads", "messenger")) {
		t.Error("a downloaded program is not offered")
	}
}

// The folder goes with the program only if the installation made it, and
// only if nothing else is left in it.
func TestRemoveProgramKeepsFoldersNotItsOwn(t *testing.T) {
	for _, c := range []struct {
		name        string
		made, other bool
		gone        bool
	}{
		{"made, empty after", true, false, true},
		{"made, something else in it", true, true, false},
		{"the user's own, empty after", false, false, false},
		{"the user's own, something else in it", false, true, false},
	} {
		dir := filepath.Join(t.TempDir(), "Folder")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(dir, "program")
		if err := os.WriteFile(exe, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		if c.other {
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := removeProgram(Installation{Dir: dir, Exe: exe, MadeFolder: c.made}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := os.Stat(exe); !os.IsNotExist(err) {
			t.Errorf("%s: the program is left", c.name)
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) != c.gone {
			t.Errorf("%s: folder removed %v, want %v", c.name, os.IsNotExist(err), c.gone)
		}
	}
}

func TestMadeFolder(t *testing.T) {
	root := t.TempDir()
	missing, existing := filepath.Join(root, "new"), filepath.Join(root, "old")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if !madeFolder(missing, Installation{}, false) {
		t.Error("a folder to be made is not counted as made")
	}
	if madeFolder(existing, Installation{}, false) {
		t.Error("a folder the user had is counted as made")
	}
	// Installed again over the copy that made the folder, it stays made.
	if !madeFolder(existing, Installation{Dir: existing, MadeFolder: true}, true) {
		t.Error("reinstalling lost the folder")
	}
	if madeFolder(existing, Installation{Dir: missing, MadeFolder: true}, true) {
		t.Error("another installation's folder counts for this one")
	}
}
