// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"testing"

	"gio-mw/defaults/schemes"

	"gioui.org/layout"

	"komarugram/internal/messenger/preferences"
)

// With "System accent color" on, the window's colors are made from the
// system's accent, follow it when it changes, and go back to the app's accent
// when it is turned off; the checkbox is offered only where the system
// tells an accent.
func TestSystemAccent(t *testing.T) {
	prefs := newSettingsPrefs(t)
	h := newSettingsApp(t, prefs, Services{}, false, image.Pt(900, 1300))
	a := h.app
	system := a.window.Appearance
	primary := func() color.NRGBA {
		var c color.NRGBA
		h.frame(func(gtx layout.Context) { c = a.Theme(gtx).Scheme.Primary.Color.AsNRGBA() })
		return c
	}
	seed := color.NRGBA{R: 61, G: 174, B: 233, A: 255}
	defaultLight := schemes.SchemeFromSeed(seed, false).Primary.Color.AsNRGBA()
	if got := primary(); got != defaultLight {
		t.Fatalf("off by default: primary %v, want %v", got, defaultLight)
	}
	h.show(settingsAppearance)
	if !a.settings.accentOffered() {
		t.Fatal("the checkbox is not offered where the system tells an accent")
	}

	a.settings.systemAccentCheckbox.SetValues([]string{"accent"})
	a.settings.setSystemAccent(true)
	if !prefs.Global().SystemAccent {
		t.Fatal("the checkbox did not save the preference")
	}
	accent, _ := system.Accent()
	if got, want := primary(), schemes.SchemeFromSeed(accent, false).Primary.Color.AsNRGBA(); got != want {
		t.Errorf("primary %v, want the accent's %v", got, want)
	}

	orange := color.NRGBA{R: 233, G: 100, B: 10, A: 255}
	system.WithAccent(orange)
	if got, want := primary(), schemes.SchemeFromSeed(orange, false).Primary.Color.AsNRGBA(); got != want {
		t.Errorf("after the system's accent changed: primary %v, want %v", got, want)
	}

	if err := prefs.SetSystemAccent(false); err != nil {
		t.Fatal(err)
	}
	if got := primary(); got != defaultLight {
		t.Errorf("turned off: primary %v, want %v", got, defaultLight)
	}
	if err := prefs.SetTheme(preferences.ThemeDark); err != nil {
		t.Fatal(err)
	}
	if got, want := primary(), schemes.SchemeFromSeed(seed, true).Primary.Color.AsNRGBA(); got != want {
		t.Errorf("dark theme: primary %v, want %v", got, want)
	}

	system.WithAccent(color.NRGBA{})
	if a.settings.accentOffered() {
		t.Error("the checkbox is offered where the system tells no accent")
	}
}

// "Neutral surface colors" makes the surfaces gray, with the app's
// accent and with the system's, and keeps the accent's colors.
func TestNeutralSurfaces(t *testing.T) {
	prefs := newSettingsPrefs(t)
	h := newSettingsApp(t, prefs, Services{}, false, image.Pt(900, 1300))
	a := h.app
	scheme := func() (surface, primary color.NRGBA) {
		h.frame(func(gtx layout.Context) {
			sc := a.Theme(gtx).Scheme
			surface, primary = sc.Surface.Color.AsNRGBA(), sc.Primary.Color.AsNRGBA()
		})
		return
	}
	gray := func(c color.NRGBA) bool { return c.R == c.G && c.G == c.B }
	if surface, _ := scheme(); gray(surface) {
		t.Fatalf("off by default: surface %v is gray", surface)
	}

	h.show(settingsAppearance)
	a.settings.neutralSurfacesCheckbox.SetValues([]string{"neutral"})
	a.settings.setNeutralSurfaces(true)
	if !prefs.Global().NeutralSurfaces {
		t.Fatal("the checkbox did not save the preference")
	}
	surface, primary := scheme()
	if !gray(surface) {
		t.Errorf("default accent: surface %v is not gray", surface)
	}
	if want := schemes.SchemeFromSeed(defaultAppAccent, false).Primary.Color.AsNRGBA(); primary != want {
		t.Errorf("default accent: primary %v, want %v", primary, want)
	}

	if err := prefs.SetSystemAccent(true); err != nil {
		t.Fatal(err)
	}
	accent, _ := a.window.Appearance.Accent()
	surface, primary = scheme()
	if !gray(surface) {
		t.Errorf("system accent: surface %v is not gray", surface)
	}
	if want := schemes.SchemeFromSeed(accent, false).Primary.Color.AsNRGBA(); primary != want {
		t.Errorf("system accent: primary %v, want the accent's %v", primary, want)
	}
}
