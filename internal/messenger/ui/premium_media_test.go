// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// premiumPageHeight is the height of the Premium page of an account with or
// without Premium, with or without the lever for the Premium address of files.
func premiumPageHeight(t *testing.T, premium model.PremiumSource, lever bool) int {
	t.Helper()
	p := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, nil,
		func() string { return "" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
	p.section = settingsPremium
	p.premium = premium
	if lever {
		media := false
		p.premiumMedia = func() bool { return media }
		p.setPremiumMedia = func(v bool) { media = v }
	}
	ops := new(op.Ops)
	var height int
	for range 3 {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Constraints{Max: image.Pt(900, 3000)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		dims := p.layoutPremium(gtx, localization.For("ru"))
		height = dims.Size.Y
	}
	return height
}

// TestPremiumMediaLeverIsForWhoHasPremium checks that the switch is on the
// Premium page of an account that has Premium, and of no other.
func TestPremiumMediaLeverIsForWhoHasPremium(t *testing.T) {
	if with, without := premiumPageHeight(t, activePremium{}, true), premiumPageHeight(t, activePremium{}, false); with <= without {
		t.Errorf("an account with Premium: %d high with the lever and %d without", with, without)
	}
	if with, without := premiumPageHeight(t, plainPremium{}, true), premiumPageHeight(t, plainPremium{}, false); with != without {
		t.Errorf("an account without Premium: %d high with the lever and %d without", with, without)
	}
}
