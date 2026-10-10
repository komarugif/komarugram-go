// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package miniapp_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"komarugram/pkg/miniapp"
)

// TestCheckBrowser checks that CheckBrowser takes the Chromium-based
// browsers and Firefox installed here and turns down other programs.
func TestCheckBrowser(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "brave-browser", "firefox"} {
		if path, err := exec.LookPath(name); err == nil {
			banner, err := miniapp.CheckBrowser(ctx, path)
			if err != nil {
				t.Errorf("%s: %v", path, err)
			}
			t.Logf("%s: %s", path, banner)
		}
	}
	for _, name := range []string{"ls", "vlc"} {
		if path, err := exec.LookPath(name); err == nil {
			if banner, err := miniapp.CheckBrowser(ctx, path); err == nil {
				t.Errorf("%s was taken for a browser: %q", path, banner)
			} else {
				t.Logf("%s: %v", path, err)
			}
		}
	}
	silent := filepath.Join(t.TempDir(), "silent")
	if err := os.WriteFile(silent, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := miniapp.CheckBrowser(ctx, silent); err == nil {
		t.Error("a program printing nothing was taken for a browser")
	}
	if _, err := miniapp.CheckBrowser(ctx, "chromium"); !errors.Is(err, miniapp.ErrNotExecutable) {
		t.Errorf("a relative path: got %v, want ErrNotExecutable", err)
	}
}
