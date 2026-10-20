// SPDX-License-Identifier: Unlicense OR MIT

// Package buildinfo tells where the program comes from: its repository,
// and the commit it was built from.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

// Repository is the program's source.
const Repository = "github.com/komarugif/komarugram-go"

// commit and modified are set by the linker where Go does not stamp the
// commit itself: a build whose main module is not this repository, as
// cmd/windows-build's is (-X komarugram/internal/buildinfo.commit=…).
var commit, modified string

// Build is the commit the program was built from.
type Build struct {
	// Commit is the full hash, empty when the build does not know it: go
	// run, or a build outside a checkout, stamps none.
	Commit string
	// Modified is whether the checkout had changes the commit lacks.
	Modified bool
}

// Short is the commit's first twelve characters, with "+" after when the
// checkout was modified; empty without a commit.
func (b Build) Short() string {
	if b.Commit == "" {
		return ""
	}
	s := b.Commit[:min(len(b.Commit), 12)]
	if b.Modified {
		s += "+"
	}
	return s
}

// Current is the commit of this build: the linker's, or else the one Go
// stamped into the binary when it was built in a checkout.
var Current = sync.OnceValue(func() Build {
	if commit != "" {
		return Build{Commit: commit, Modified: modified == "true"}
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Build{}
	}
	return fromSettings(info.Settings)
})

func fromSettings(settings []debug.BuildSetting) Build {
	var b Build
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			b.Commit = s.Value
		case "vcs.modified":
			b.Modified = s.Value == "true"
		}
	}
	return b
}
