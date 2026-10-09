// SPDX-License-Identifier: Unlicense OR MIT

package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// home makes a home of the test's own, with a desktop named as
// xdg-user-dirs names it in Russian.
func home(t *testing.T) (desktop string) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	desktop = filepath.Join(h, "Рабочий стол")
	if err := os.MkdirAll(filepath.Join(h, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := "# written by xdg-user-dirs-update\nXDG_DESKTOP_DIR=\"$HOME/Рабочий стол\"\nXDG_DOWNLOAD_DIR=\"$HOME/Загрузки\"\n"
	if err := os.WriteFile(filepath.Join(h, ".config", "user-dirs.dirs"), []byte(dirs), 0o644); err != nil {
		t.Fatal(err)
	}
	return desktop
}

func TestInstallAndUninstall(t *testing.T) {
	desktop := home(t)
	dir := filepath.Join(t.TempDir(), "with space", appName)
	exe, err := Install(context.Background(), Options{Dir: dir, Desktop: true, Menu: true})
	if err != nil {
		t.Fatal(err)
	}
	if exe != filepath.Join(dir, exeName) {
		t.Fatalf("installed to %s", exe)
	}
	if info, err := os.Stat(exe); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("program: %v, %v", info, err)
	}
	entry, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".local", "share", "applications", id+".desktop"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Name=KomaruGram Go\n", "Exec=\"" + exe + "\"\n", "Exec=\"" + exe + "\" -uninstall\n", "Icon=" + filepath.Join(dir, iconName) + "\n"} {
		if !strings.Contains(string(entry), want) {
			t.Errorf("desktop file lacks %q:\n%s", want, entry)
		}
	}
	if strings.Contains(string(entry), "NoDisplay") {
		t.Error("menu entry is hidden")
	}
	if info, err := os.Stat(filepath.Join(desktop, id+".desktop")); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("desktop launcher: %v, %v", info, err)
	}
	// On the desktop the short name.
	if launcher, _ := os.ReadFile(filepath.Join(desktop, id+".desktop")); !strings.Contains(string(launcher), "Name=KomaruGram\n") {
		t.Errorf("desktop launcher's name:\n%s", launcher)
	}
	inst, ok := Find()
	if !ok || inst.Exe != exe || inst.Dir != dir {
		t.Fatalf("Find: %+v, %v", inst, ok)
	}

	// Something of the user's own in the folder stays, and so does it.
	mine := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(mine, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(context.Background(), inst, false); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{exe, filepath.Join(dir, iconName), filepath.Join(desktop, id+".desktop"), entryPath()} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is left: %v", gone, err)
		}
	}
	if _, err := os.Stat(mine); err != nil {
		t.Errorf("the user's file was removed: %v", err)
	}
	if _, ok := Find(); ok {
		t.Error("found after the removal")
	}
}

// Without the menu entry the program is still registered, hidden, so
// that it can be found and removed.
func TestInstallWithoutMenuOrDesktop(t *testing.T) {
	desktop := home(t)
	dir := filepath.Join(t.TempDir(), appName)
	if _, err := Install(context.Background(), Options{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(entryPath())
	if err != nil || !strings.Contains(string(entry), "NoDisplay=true\n") {
		t.Fatalf("hidden entry: %v\n%s", err, entry)
	}
	if _, err := os.Stat(filepath.Join(desktop, id+".desktop")); !os.IsNotExist(err) {
		t.Errorf("desktop launcher made: %v", err)
	}
	inst, ok := Find()
	if !ok {
		t.Fatal("not found")
	}
	if err := Uninstall(context.Background(), inst, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("empty folder left: %v", err)
	}
}

func TestInstallToReadOnlyFolder(t *testing.T) {
	home(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })
	dir := filepath.Join(parent, appName)
	if !NeedsAdmin(dir) {
		t.Skip("the folder is writable: running as root?")
	}
	if _, err := Install(context.Background(), Options{Dir: dir}); err != ErrNeedsAdmin {
		t.Fatalf("Install: %v, want ErrNeedsAdmin", err)
	}
}

func TestExecQuote(t *testing.T) {
	for path, want := range map[string]string{
		"/home/u/KomaruGram/komarugram": `"/home/u/KomaruGram/komarugram"`,
		`/a b/100%/$x"y`:                `"/a b/100%%/\\$x\\"y"`,
		`/a\b`:                          `"/a\\\\b"`,
	} {
		if got := execQuote(path); got != want {
			t.Errorf("execQuote(%q) = %s, want %s", path, got, want)
		}
	}
}

func TestUserDirsDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user-dirs.dirs")
	for content, want := range map[string]string{
		"XDG_DESKTOP_DIR=\"$HOME/Desktop\"\n": "/h/Desktop",
		"XDG_DESKTOP_DIR=\"$HOME/\"\n":        "",
		"XDG_DESKTOP_DIR=\"/data/Стол\"\n":    "/data/Стол",
		"# nothing\n":                         "",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := userDirsDesktop(path, "/h"); got != want {
			t.Errorf("%q: %q, want %q", content, got, want)
		}
	}
}

// Installed into a folder the user had, the removal leaves the folder,
// even with nothing left in it.
func TestUninstallKeepsTheUsersFolder(t *testing.T) {
	home(t)
	dir := filepath.Join(t.TempDir(), "Мои программы")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), Options{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	inst, ok := Find()
	if !ok || inst.MadeFolder {
		t.Fatalf("Find: %+v, %v", inst, ok)
	}
	if err := Uninstall(context.Background(), inst, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inst.Exe); !os.IsNotExist(err) {
		t.Errorf("the program is left: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the user's folder was removed: %v", err)
	}
}
