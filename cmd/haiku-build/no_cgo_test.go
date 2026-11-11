// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"runtime"
	"strings"
	"testing"

	"komarugram/pkg/program"
)

// haiku-build links with no flag: Haiku's ld refuses what Go hands it for a
// program with cgo in it, so nothing it imports has any, as pkg/program once
// had to watch memory (it is in pkg/program/haikumem now), and a build by
// "go run ./cmd/haiku-build" failed at its link.
func TestHaikuBuildImportsNoCgo(t *testing.T) {
	if runtime.GOOS != "haiku" {
		t.Skip("only on Haiku, where cgo is linked by its ld")
	}
	out, err := program.Command("go", "list", "-deps", "-f", "{{if .CgoFiles}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	if cgo := strings.Fields(string(out)); len(cgo) > 0 {
		t.Errorf("haiku-build imports cgo: %s", strings.Join(cgo, ", "))
	}
}
