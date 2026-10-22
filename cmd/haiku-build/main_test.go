// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"os"
	"strings"
	"testing"
)

func TestLowMemoryDefaultsReachGoCommands(t *testing.T) {
	for _, key := range []string{"GOMAXPROCS", "GOGC", "GOMEMLIMIT"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("GOFLAGS", "-trimpath")
	if err := enableLowMemory(); err != nil {
		t.Fatal(err)
	}
	env := commandEnv(goEnv("/tmp/haiku-work/messenger.mod"))
	for key, want := range map[string]string{
		"GOMAXPROCS": "1", "GOGC": "50", "GOMEMLIMIT": "1792MiB",
		"CGO_ENABLED": "1", "GOWORK": "off",
		"GOFLAGS": "-trimpath -p=1 -gcflags=github.com/gotd/td/tg=-l -modfile=/tmp/haiku-work/messenger.mod",
	} {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLowMemoryPreservesExplicitLimits(t *testing.T) {
	for key, value := range map[string]string{
		"GOMAXPROCS": "2", "GOGC": "25", "GOMEMLIMIT": "1500MiB", "GOFLAGS": "-trimpath",
	} {
		t.Setenv(key, value)
	}
	if err := enableLowMemory(); err != nil {
		t.Fatal(err)
	}
	env := commandEnv(goEnv("/tmp/haiku-work/messenger.mod"))
	for key, want := range map[string]string{"GOMAXPROCS": "2", "GOGC": "25", "GOMEMLIMIT": "1500MiB"} {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want explicit %q", key, got, want)
		}
	}
}

// The copy of go.mod names the Go of go-haiku when go.mod asks for a newer
// one, which that Go would refuse, and keeps go.mod's otherwise.
func TestModForTheGoOfHaiku(t *testing.T) {
	goMod := "module komarugram\n\ngo 1.27.3\n\nrequire x v1.0.0\n"
	for _, c := range []struct{ running, want string }{
		{"go1.27.1", "go 1.27.1"},
		{"go1.27.1-haiku1", "go 1.27.1"},
		{"go1.27.3", "go 1.27.3"},
		{"go1.28.0", "go 1.27.3"},
		{"devel +abc", "go 1.27.3"},
	} {
		got := modFor(goMod, c.running)
		if !strings.Contains(got, "\n"+c.want+"\n") || !strings.Contains(got, "require x v1.0.0") {
			t.Errorf("for %s:\n%s", c.running, got)
		}
	}
}

// Environment entries appended later override earlier values in exec.Cmd.
func commandEnv(entries []string) map[string]string {
	out := map[string]string{}
	for _, entry := range entries {
		if key, value, ok := strings.Cut(entry, "="); ok {
			out[key] = value
		}
	}
	return out
}
