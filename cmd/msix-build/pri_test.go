// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestWritePRISources writes the files resources.pri is made from, the way
// docs/BUILD_MSIX.md tells to regenerate it, when MSIX_PRI_DIR is set.
func TestWritePRISources(t *testing.T) {
	dir := os.Getenv("MSIX_PRI_DIR")
	if dir == "" {
		t.Skip("MSIX_PRI_DIR is not set")
	}
	logos, err := logoParts()
	if err != nil {
		t.Fatal(err)
	}
	// What the index is made from is the names, and the manifest's identity.
	for _, p := range logos {
		path := filepath.Join(dir, filepath.FromSlash(p.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, p.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	xml, err := manifest(manifestInfo{Name: "KomaruGram", Publisher: defaultPublisher, PublisherDisplay: "KomaruGram", Version: "1.0.0.0", Arch: "x64"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AppxManifest.xml"), xml, 0o644); err != nil {
		t.Fatal(err)
	}
}

// resources.pri names the files it picks from: when logoNames changes, it
// has to be made again, or the system looks for files that are not there.
func TestResourcesPRIHasEveryLogo(t *testing.T) {
	for name := range logoNames() {
		if !bytes.Contains(resourcesPRI, []byte(`Assets\`+name)) {
			t.Errorf("resources.pri does not know %s: make it again", name)
		}
	}
	if !bytes.Contains(resourcesPRI, utf16("KomaruGram")) {
		t.Error("resources.pri is not of the map KomaruGram")
	}
}

func utf16(s string) []byte {
	var b []byte
	for _, r := range s {
		b = append(b, byte(r), 0)
	}
	return b
}

// The package carries the index, and the block map knows it.
func TestPackageHoldsResourcesPRI(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "KomaruGram.exe")
	os.WriteFile(exe, []byte("MZ"), 0o644)
	out := filepath.Join(dir, "p.msix")
	if err := build(options{out: out, arch: "amd64", exe: exe, version: "1.0.0.0", name: defaultName, display: "KomaruGram", signingDir: filepath.Join(dir, "k")}); err != nil {
		t.Fatal(err)
	}
	pkg, _ := os.ReadFile(out)
	files := readZip(t, pkg)
	if !bytes.Equal(files["resources.pri"], resourcesPRI) {
		t.Error("resources.pri is not in the package")
	}
	if !bytes.Contains(files[blockMapName], []byte(`Name="resources.pri"`)) {
		t.Error("the block map does not list resources.pri")
	}
}
