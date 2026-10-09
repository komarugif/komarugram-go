// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	"komarugram/pkg/program"
)

// chooseFolder asks for a folder in the system's own chooser, opened at
// start: Windows' folder browser, or kdialog or zenity on Linux. A
// cancelled chooser chooses "".
func chooseFolder(ctx context.Context, start string) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.FolderBrowserDialog; `
		if start != "" {
			script += `$dialog.SelectedPath = '` + strings.ReplaceAll(start, `'`, `''`) + `'; `
		}
		script += `if ($dialog.ShowDialog() -eq 'OK') { $dialog.SelectedPath }`
		cmd = powershellChooser(ctx, script)
	case "linux":
		if _, err := exec.LookPath("kdialog"); err == nil {
			cmd = program.CommandContext(ctx, "kdialog", "--getexistingdirectory", start)
		} else if _, err := exec.LookPath("zenity"); err == nil {
			cmd = program.CommandContext(ctx, "zenity", "--file-selection", "--directory", "--filename="+start+"/")
		} else {
			return "", errNoChooser
		}
	default:
		return "", errNoChooser
	}
	out, err := chooserOutput(cmd)
	if err != nil {
		return "", err
	}
	if paths := splitPaths(out); len(paths) > 0 {
		return paths[0], nil
	}
	return "", nil
}
