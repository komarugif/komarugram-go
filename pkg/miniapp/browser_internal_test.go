// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"slices"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for banner, want := range map[string]version{
		"Chromium 152.0.7977.82 for Linux Mint":    {152, 0, 7977, 82},
		"Chromium 140.0.7339.80 built on Debian":   {140, 0, 7339, 80},
		"Google Chrome 140.0.7339.80":              {140, 0, 7339, 80},
		"Brave Browser 148.1.90.124":               {148},
		"Brave Browser Beta 148.1.91.158 beta":     {148},
		"Chromium 152.0.7977.82 snap":              {152, 0, 7977, 82},
		"Mozilla Firefox 140.3.0esr":               {140, 3, 0},
		"LibreWolf 157.0-1":                        {157, 0},
		"":                                         nil,
		"some wrapper script says hello":           nil,
		"Chromium version unknown, see chrome://x": nil,
	} {
		if got := parseVersion(banner); !slices.Equal(got, want) {
			t.Errorf("parseVersion(%q) = %v, want %v", banner, got, want)
		}
	}
}

func TestNewest(t *testing.T) {
	native := browser{ref: "/usr/bin/chromium", found: true, version: version{140, 0, 7339, 80}}
	flatpak := browser{ref: "org.chromium.Chromium", flatpak: true, found: true, version: version{140, 0, 7339, 127}}
	brave := browser{ref: "/usr/bin/brave-browser", found: true, version: version{140}}
	braveNext := browser{ref: "com.brave.Browser", flatpak: true, found: true, version: version{141}}
	silent := browser{ref: "/usr/bin/google-chrome", found: true}
	missing := browser{}

	for _, test := range []struct {
		name  string
		found []browser
		want  string
	}{
		{"a newer flatpak beats the distribution's package", []browser{native, missing, flatpak}, flatpak.ref},
		{"an older flatpak loses to it", []browser{flatpak, native}, flatpak.ref},
		{"a tie keeps the earlier one", []browser{native, brave}, native.ref},
		{"a newer major wins even when only the major is known", []browser{flatpak, braveNext}, braveNext.ref},
		{"an unknown version loses to any known one", []browser{silent, native}, native.ref},
		{"an unknown version is still used when alone", []browser{missing, silent}, silent.ref},
		{"nothing found", []browser{missing}, ""},
	} {
		if got := newest(test.found); got.ref != test.want {
			t.Errorf("%s: chose %q, want %q", test.name, got.ref, test.want)
		}
	}
}

// TestFindBrowser reports what this machine offers; it asserts only that a
// browser found is one that told its version, or, built from Firefox, its
// name: the version of its engine may come only once it runs.
func TestFindBrowser(t *testing.T) {
	for _, b := range candidates() {
		if b.found {
			t.Logf("%s: %q → %v", b.ref, b.banner, b.version)
		}
	}
	chosen := findBrowser()
	if !chosen.found {
		t.Skip("no Chromium-based browser found")
	}
	t.Logf("chosen: %s (%s)", chosen.ref, chosen.banner)
	if chosen.version == nil && chosen.engine != engineFirefox {
		t.Errorf("%s did not report a version", chosen.ref)
	}
}

func TestChromiumVersion(t *testing.T) {
	for banner, want := range map[string]bool{
		"Chromium 152.0.7977.82 for Linux Mint": true,
		"Google Chrome 140.0.7339.80":           true,
		"Brave Browser 148.1.90.124":            true,
		"Microsoft Edge 140.0.3485.54":          true,
		"Mozilla Firefox 143.0.1":               false,
		"rm (GNU coreutils) 9.4":                false,
		"VLC version 3.0.20 Vetinari":           false,
		"":                                      false,
	} {
		if got := chromiumVersion.MatchString(banner); got != want {
			t.Errorf("%q: got %t, want %t", banner, got, want)
		}
	}
}
