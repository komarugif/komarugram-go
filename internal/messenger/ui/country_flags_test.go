// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// TestCountryListHidesFlagsThatAreBoxes checks the column of flags in the
// list of countries: it is there where the fonts show a flag, as a picture or
// as the letters of its country, as Windows' font does, and not where they
// show the box of a character they lack, as a system without an emoji font
// does.
func TestCountryListHidesFlagsThatAreBoxes(t *testing.T) {
	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(600, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}

	// Fonts with no emoji at all.
	bare := defaults.NewTheme(gtx, schemes.SchemeBaselineLight())
	bare.TextShaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	wdk.InitMaterialThemeInContext(gtx, bare)
	if newCountryPicker().showsFlags(gtx) {
		t.Error("a column of boxes where the fonts have no emoji")
	}

	// The window's own fonts, where there is an emoji font: flags or letters.
	window := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	a := New(window, mockstore.New(time.Now(), 0), Services{Preferences: preferences.Memory(), MiniApps: miniappprefs.New(miniapp.Ephemeral)})
	defer func() {
		a.Close()
		window.Motion.Close()
	}()
	gtx = layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(600, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, a.Theme(gtx))
	var d emojiDrawn
	d.use(a.Theme(gtx))
	if !d.draws("\U0001F600") {
		t.Skip("no emoji font")
	}
	if !newCountryPicker().showsFlags(gtx) {
		t.Error("no flags where the fonts have emoji, even if only the letters of the flags")
	}
}
