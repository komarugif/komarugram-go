// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every program the client starts goes through Command or CommandContext,
// so that on Windows none of them opens a console window: a start through
// os/exec directly is one that would. The tools that build the libraries
// of Haiku are not the client.
func TestEveryStartGoesThroughCommand(t *testing.T) {
	for _, root := range []string{"../../internal", "../../pkg", "../../cmd/messenger"} {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
				filepath.Base(path) == "build.go" || filepath.Dir(path) == filepath.Clean("../../pkg/program") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "exec.Command(") || strings.Contains(line, "exec.CommandContext(") {
					t.Errorf("%s:%d starts a program through os/exec: use program.Command", path, i+1)
				}
			}
			return nil
		})
	}
}
