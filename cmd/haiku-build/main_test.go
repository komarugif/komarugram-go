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
	env := commandEnv(goEnv("/tmp/haiku-work/go.work"))
	for key, want := range map[string]string{
		"GOMAXPROCS": "1", "GOGC": "50", "GOMEMLIMIT": "1792MiB",
		"CGO_ENABLED": "1", "GOWORK": "/tmp/haiku-work/go.work",
		"GOFLAGS": "-trimpath -p=1 -gcflags=github.com/gotd/td/tg=-l",
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
	env := commandEnv(goEnv("/tmp/haiku-work/go.work"))
	for key, want := range map[string]string{"GOMAXPROCS": "2", "GOGC": "25", "GOMEMLIMIT": "1500MiB"} {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want explicit %q", key, got, want)
		}
	}
}

// go.work asks for the Go go.mod does: Go refuses one that asks for less.
func TestWorkTakesTheGoOfGoMod(t *testing.T) {
	for _, goMod := range []string{
		"module komarugram\n\ngo 1.29.4\n\nrequire x v1.0.0\n",
		"module komarugram\r\n\r\ngo 1.29.4\r\n",
	} {
		if got := modGoLine(goMod); got != "go 1.29.4" {
			t.Errorf("go line of %q = %q, want %q", goMod, got, "go 1.29.4")
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
