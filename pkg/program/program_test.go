// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package program

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFlatpakApp(t *testing.T) {
	for text, want := range map[string]string{
		"#!/bin/sh\nexec /usr/bin/flatpak run --branch=stable --arch=x86_64 org.videolan.VLC \"$@\"\n": "org.videolan.VLC",
		"#!/bin/sh\nflatpak run io.mpv.Mpv \"$@\"\n":                                                   "io.mpv.Mpv",
		"#!/bin/sh\nexec /usr/bin/vlc \"$@\"\n":                                                        "",
		"#!/bin/sh\nexec flatpak run --command=sh\n":                                                   "",
		"\x7fELF binary": "",
	} {
		id, ok := FlatpakApp(write(t, text))
		if id != want || ok != (want != "") {
			t.Errorf("%q: got %q, %t; want %q", text, id, ok, want)
		}
	}
}

// TestBanner checks that Banner keeps the first line and no more than its
// limit of a program that prints without end.
func TestBanner(t *testing.T) {
	got, err := Banner(context.Background(), write(t, "#!/bin/sh\necho\necho '  Program 1.2.3  '\nyes | head -c 10000000\n"))
	if err != nil || got != "Program 1.2.3" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := Banner(context.Background(), write(t, "#!/bin/sh\nexit 0\n")); err != ErrNoBanner {
		t.Errorf("silent program: got %v, want ErrNoBanner", err)
	}
	var b limitedBuffer
	b.limit = 4
	b.Write([]byte("abcdef"))
	b.Write([]byte("gh"))
	if b.String() != "abcd" {
		t.Errorf("limited buffer kept %q", b.String())
	}
}

func TestDottedVersion(t *testing.T) {
	for in, want := range map[string]string{
		"3,0,24,0":        "3.0.24.0",
		"3, 0, 24, 0":     "3.0.24.0",
		"150.0.7871.255 ": "150.0.7871.255",
		"3.0.20":          "3.0.20",
	} {
		if got := dottedVersion(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
