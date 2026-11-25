// SPDX-License-Identifier: Unlicense OR MIT

// Package assets holds the files of this directory the client builds in.
package assets

import (
	_ "embed"
	"strings"
)

// LogoRound is logo_round.png, the default application icon.
//
//go:embed logo_round.png
var LogoRound []byte

// LogoRoundNewYear is logo_round_newyear.png, the New Year application icon.
//
//go:embed logo_round_newyear.png
var LogoRoundNewYear []byte

// TrayMonochrome is tray_monochrome.svg, the monochrome system tray icon.
//
//go:embed tray_monochrome.svg
var TrayMonochrome []byte

// Logo is logo.svg, the square logo, for drawing at any size. Its shapes
// are what oksvg reads: a gradient keeps its stops itself, not through
// xlink:href.
//
//go:embed logo.svg
var Logo []byte

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
