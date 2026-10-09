// SPDX-License-Identifier: Unlicense OR MIT

package install

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"komarugram/assets"
	"komarugram/pkg/program"
)

const supported = true

// On Linux the program is komarugram in its folder, with its icon beside
// it: the logo is not square, so it has no place among hicolor's sizes,
// and the desktop file names it by its path.
const (
	exeName  = "komarugram"
	iconName = "komarugram.png"
	// exeKey is the desktop file's own key with the installed program's
	// path, which Find reads; madeKey tells that the installation made
	// its folder.
	exeKey  = "X-KomaruGram-Exe"
	madeKey = "X-KomaruGram-Made-Folder"
)

func userDir() string {
	return filepath.Join(dataHome(), appName)
}

// dataHome is $XDG_DATA_HOME, ~/.local/share by default.
func dataHome() string {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

// entryPath is the desktop file of the applications menu. It is also the
// registration: without the menu entry it is there, hidden.
func entryPath() string {
	return filepath.Join(dataHome(), "applications", id+".desktop")
}

// desktopDir is the user's desktop, as xdg-user-dirs names it.
func desktopDir() string {
	home, _ := os.UserHomeDir()
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	if dir := userDirsDesktop(filepath.Join(config, "user-dirs.dirs"), home); dir != "" {
		return dir
	}
	return filepath.Join(home, "Desktop")
}

// userDirsDesktop reads XDG_DESKTOP_DIR from the user-dirs.dirs file at
// path: a line such as XDG_DESKTOP_DIR="$HOME/Рабочий стол".
func userDirsDesktop(path, home string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		value, ok := strings.CutPrefix(line, "XDG_DESKTOP_DIR=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		value = strings.ReplaceAll(value, `\"`, `"`)
		if rest, ok := strings.CutPrefix(value, "$HOME"); ok {
			value = home + rest
		}
		if !filepath.IsAbs(value) || filepath.Clean(value) == filepath.Clean(home) {
			// xdg-user-dirs points a directory it has none for at home.
			return ""
		}
		return value
	}
	return ""
}

func shortcutPath() string {
	return filepath.Join(desktopDir(), id+".desktop")
}

func install(ctx context.Context, self, dir string, o Options) (string, error) {
	if err := writable(dir); err != nil {
		return "", err
	}
	existing, installed := Find()
	made := madeFolder(dir, existing, installed)
	exe := filepath.Join(dir, exeName)
	if err := copyProgram(self, exe); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, iconName), assets.LogoRound, 0o644); err != nil {
		return "", err
	}
	entry := desktopEntry(listName, exe, filepath.Join(dir, iconName), !o.Menu, made)
	if err := writeEntry(entryPath(), entry, 0o644); err != nil {
		return "", err
	}
	shortcut := shortcutPath()
	if o.Desktop {
		if err := writeEntry(shortcut, desktopEntry(appName, exe, filepath.Join(dir, iconName), false, made), 0o755); err != nil {
			return "", err
		}
		trust(ctx, shortcut)
	} else if err := os.Remove(shortcut); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return exe, nil
}

func writeEntry(path, entry string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(entry), mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// trust marks the launcher on the desktop as one the user allowed, as the
// file managers do when asked: GNOME's by metadata::trusted, Xfce's by
// the checksum of the file. Without it they ask before the first start.
func trust(ctx context.Context, path string) {
	if _, err := exec.LookPath("gio"); err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	for _, args := range [][]string{
		{"set", path, "metadata::trusted", "true"},
		{"set", "-t", "string", path, "metadata::xfce-exe-checksum", hex.EncodeToString(sum[:])},
	} {
		_ = program.CommandContext(ctx, "gio", args...).Run()
	}
}

// desktopEntry is the desktop file of the program exe, called name,
// hidden from the menu when hidden is set; its action removes the
// program. made records that the installation made the program's folder.
func desktopEntry(name, exe, icon string, hidden, made bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Desktop Entry]\nType=Application\nName=%s\n", name)
	b.WriteString("Comment=Telegram client\nComment[ru]=Клиент Telegram\n")
	fmt.Fprintf(&b, "Exec=%s\n", execQuote(exe))
	fmt.Fprintf(&b, "Icon=%s\n", icon)
	b.WriteString("Terminal=false\nCategories=Network;InstantMessaging;Chat;\n")
	if hidden {
		b.WriteString("NoDisplay=true\n")
	}
	fmt.Fprintf(&b, "%s=%s\n", exeKey, exe)
	fmt.Fprintf(&b, "%s=%t\n", madeKey, made)
	b.WriteString("Actions=uninstall;\n\n[Desktop Action uninstall]\n")
	b.WriteString("Name=Uninstall\nName[ru]=Удалить\n")
	fmt.Fprintf(&b, "Exec=%s -uninstall\n", execQuote(exe))
	return b.String()
}

// execQuote quotes a path for a desktop file's Exec key: in double quotes,
// with `"`, "`", "$" and "\" escaped, and "%" doubled, as the Desktop
// Entry Specification asks.
func execQuote(path string) string {
	r := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", `$`, `\\$`, `%`, `%%`)
	return `"` + r.Replace(path) + `"`
}

// Find is the installed copy, from the desktop file that registers it.
func Find() (Installation, bool) {
	f, err := os.Open(entryPath())
	if err != nil {
		return Installation{}, false
	}
	defer f.Close()
	var inst Installation
	s := bufio.NewScanner(f)
	for s.Scan() {
		if exe, ok := strings.CutPrefix(s.Text(), exeKey+"="); ok && filepath.IsAbs(exe) {
			inst.Dir, inst.Exe = filepath.Dir(exe), exe
		}
		if made, ok := strings.CutPrefix(s.Text(), madeKey+"="); ok {
			inst.MadeFolder = made == "true"
		}
	}
	return inst, inst.Exe != ""
}

func uninstall(ctx context.Context, inst Installation) error {
	if err := removeProgram(inst, iconName); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return ErrNeedsAdmin
		}
		return err
	}
	for _, path := range []string{shortcutPath(), entryPath()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
