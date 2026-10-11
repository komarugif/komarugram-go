// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"komarugram/pkg/program"
)

// The AppImage launcher must handle paths with spaces and forward arguments
// verbatim without changing the caller's directory (relative file arguments).
func TestAppRunPreservesArgumentsAndDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("AppImage's launcher runs on Linux")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "App Dir")
	if err := prepareAppDir(dir, repo); err != nil {
		t.Fatal(err)
	}
	probe := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "usr", "bin", "komarugram"), []byte(probe), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "working directory")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CDPATH", t.TempDir())
	args := []string{"-demo", "file with spaces", "$HOME; echo literal", ""}
	cmd := program.Command(filepath.Join(dir, "AppRun"), args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher: %v\n%s", err, out)
	}
	want := strings.Join(append([]string{cwd}, args...), "\n") + "\n"
	if string(out) != want {
		t.Fatalf("launcher output %q, want %q", out, want)
	}
}

func TestFailedPackagerLeavesExistingOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("AppImage builds on Linux")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "previous.AppImage")
	const previous = "previous working image"
	if err := os.WriteFile(out, []byte(previous), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(dir, "failing-packager")
	// A packager may write part of an image before failing. It must never
	// receive the path of an existing working image as its destination.
	probe := "#!/bin/sh\nfor output in \"$@\"; do :; done\nprintf partial > \"$output\"\nexit 7\n"
	if err := os.WriteFile(tool, []byte(probe), 0o755); err != nil {
		t.Fatal(err)
	}
	o := options{out: out, tool: tool}
	if err := packageAppImage(o, filepath.Join(dir, "AppDir"), dir, "x86_64", os.Environ()); err == nil {
		t.Fatal("failed packager reported success")
	}
	data, err := os.ReadFile(out)
	if err != nil || string(data) != previous {
		t.Fatalf("existing output changed: %q, %v", data, err)
	}
}
