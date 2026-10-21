// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"komarugram/pkg/program"
)

// chooseSavePath asks, in the system's dialog, where to save a file named
// name, in the user's downloads at first; kind names the files of its
// extension in the dialog's filter ("ZIP archives"). An empty path means the
// user cancelled. A path chosen without the extension gets it.
func chooseSavePath(ctx context.Context, name, kind string) (string, error) {
	dir := downloadsDir()
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	suggested := filepath.Join(dir, name)
	ext := filepath.Ext(name)
	pattern := "*" + ext
	if ext == "" {
		pattern = "*"
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// The name and the filter come from the environment, never as
		// script source.
		cmd = powershellChooser(ctx, `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.SaveFileDialog; $dialog.Filter = $env:KOMARUGRAM_SAVE_FILTER; $dialog.DefaultExt = $env:KOMARUGRAM_SAVE_EXT; $dialog.FileName = $env:KOMARUGRAM_SAVE_NAME; if ($dialog.ShowDialog() -eq 'OK') { $dialog.FileName }`)
		cmd.Env = append(os.Environ(), "KOMARUGRAM_SAVE_NAME="+suggested, "KOMARUGRAM_SAVE_EXT="+strings.TrimPrefix(ext, "."),
			"KOMARUGRAM_SAVE_FILTER="+strings.ReplaceAll(kind, "|", " ")+" ("+pattern+")|"+pattern)
	case "darwin":
		cmd = program.CommandContext(ctx, "osascript", "-e", `on run argv`, "-e", `POSIX path of (choose file name with default name (item 1 of argv))`, "-e", `end run`, name)
	case "haiku":
		cmd = program.CommandContext(ctx, "filepanel", "--save", "--directory", dir, "--name", name)
	default:
		if _, err := exec.LookPath("kdialog"); err == nil {
			cmd = program.CommandContext(ctx, "kdialog", "--getsavefilename", suggested, pattern+"|"+kind)
		} else if _, err := exec.LookPath("zenity"); err == nil {
			cmd = program.CommandContext(ctx, "zenity", "--file-selection", "--save", "--confirm-overwrite", "--filename="+suggested, "--file-filter="+kind+" | "+pattern)
		} else {
			return "", errors.New("file chooser unavailable")
		}
	}
	out, err := chooserOutput(cmd)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return "", err
	}
	// splitPaths drops the byte order mark PowerShell 2.0 starts UTF-8 with.
	paths := splitPaths(out)
	if len(paths) == 0 || strings.TrimSpace(paths[0]) == "" {
		return "", nil
	}
	path := strings.TrimSpace(paths[0])
	if ext != "" && !strings.EqualFold(filepath.Ext(path), ext) {
		path += ext
	}
	return path, nil
}
