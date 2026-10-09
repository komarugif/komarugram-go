// SPDX-License-Identifier: Unlicense OR MIT

// Package assets holds the files of this directory the client builds in.
package assets

import (
	_ "embed"
	"strings"
)

// LogoRound is logo_round.png, the application's icon.
//
//go:embed logo_round.png
var LogoRound []byte

//go:embed dependencies.txt
var dependencies string

// Dependency is a module the program is built with, as the settings name it.
type Dependency struct {
	Name, Module, Version string
}

// Dependencies are the modules of dependencies.txt, in its order.
func Dependencies() []Dependency {
	var deps []Dependency
	for _, line := range strings.Split(dependencies, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		deps = append(deps, Dependency{Name: fields[0], Module: fields[1], Version: fields[2]})
	}
	return deps
}
