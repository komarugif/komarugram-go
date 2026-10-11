// SPDX-License-Identifier: Unlicense OR MIT

package install

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// The registration is written and read under a key of the test's own.
func TestRegisterAndFind(t *testing.T) {
	saved := uninstallKey
	uninstallKey = saved + "-test"
	t.Cleanup(func() {
		registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
		uninstallKey = saved
	})
	exe := filepath.Join(t.TempDir(), "Программы", exeName)
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, make([]byte, 4096), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := register(registry.CURRENT_USER, exe, true); err != nil {
		t.Fatal(err)
	}
	inst, ok := Find()
	if !ok || inst.Exe != exe || inst.Dir != filepath.Dir(exe) || inst.System || !inst.MadeFolder {
		t.Fatalf("Find: %+v, %v", inst, ok)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if s, _, _ := k.GetStringValue("UninstallString"); s != exe+" -uninstall" && s != `"`+exe+`" -uninstall` {
		t.Errorf("UninstallString %q", s)
	}
	if name, _, _ := k.GetStringValue("DisplayName"); name != "KomaruGram Go" {
		t.Errorf("DisplayName %q", name)
	}
	if size, _, _ := k.GetIntegerValue("EstimatedSize"); size != 4 {
		t.Errorf("EstimatedSize %d KB", size)
	}
}

func TestMakeShortcut(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Ярлык", appName+".lnk")
	if err := makeShortcut(path, exe, filepath.Dir(exe)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A shell link starts with its header's size, 0x4C, and the class ID
	// of ShellLink.
	if len(data) < 20 || !bytes.Equal(data[:4], []byte{0x4c, 0, 0, 0}) {
		t.Fatalf("not a shell link: % x", data[:min(20, len(data))])
	}
}
