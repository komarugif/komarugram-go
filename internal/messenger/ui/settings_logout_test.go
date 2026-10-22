// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp/appearance"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// The confirmation of logging out opens out of its row at the end of the
// page, which a page scrolled to the row scrolls with: its buttons were
// below the window, to scroll to.
func TestLogOutConfirmationInView(t *testing.T) {
	accounts := staticAccounts{{ID: "1", UserID: 1, Name: "Ада Лавлейс", Username: "ada"}}
	p := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, accounts,
		func() string { return "1" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
	p.images = new(imageOps)
	l := localization.For("ru")
	size := image.Pt(900, 600)
	now := time.Unix(1000, 0)
	frame := func() {
		gtx := layout.Context{Ops: new(op.Ops), Now: now, Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		p.Update(gtx, themeAuto, "ru")
		p.Layout(gtx, themeAuto, appearance.Light, false, l)
		now = now.Add(16 * time.Millisecond)
	}
	frame()
	// Scrolled to the end, where the row is.
	p.list.Position.Offset = 1 << 20
	frame()
	frame()
	if p.list.Position.Length <= size.Y {
		t.Fatal("the page fits the window: nothing to scroll")
	}
	p.confirmingLogOut, p.revealLogOut = true, true
	for range 40 {
		frame()
	}
	bottom := p.list.Position.Length - p.belowLogOut - 24
	if end := p.list.Position.Offset + size.Y; end < bottom {
		t.Fatalf("the confirmation ends at %d, the window at %d", bottom, end)
	}
	if p.revealLogOut {
		t.Fatal("the page still follows the card once it has opened")
	}
}
