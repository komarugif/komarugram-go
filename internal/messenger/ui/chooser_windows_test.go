// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"os"
	"testing"
)

// A Cyrillic name a chooser prints comes back as it is, in Windows 7's
// Russian code page too: the name goes in through the environment, as the
// archive's does, and out as the chosen path would.
func TestPowershellChooserReadsCyrillic(t *testing.T) {
	const name = `C:\Users\Пользователь\Загрузки\Набор стикеров komaru GIF.zip`
	cmd := powershellChooser(context.Background(), `$env:KOMARUGRAM_TEST_NAME`)
	cmd.Env = append(os.Environ(), "KOMARUGRAM_TEST_NAME="+name)
	out, err := chooserOutput(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if paths := splitPaths(out); len(paths) != 1 || paths[0] != name {
		t.Fatalf("paths %q, want %q", paths, name)
	}
}
