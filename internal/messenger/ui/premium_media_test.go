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
	"komarugram/internal/messenger/proxy"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// premiumPageHeight is the height of the Premium page of an account with or
// without Premium, with or without the lever for the Premium address of files,
// connected through route, directly where it is empty.
func premiumPageHeight(t *testing.T, premium model.PremiumSource, lever bool, route proxy.Proxy) int {
	t.Helper()
	p := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, nil,
		func() string { return "" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
	p.section = settingsPremium
	p.premium = premium
	store := proxy.NewStore("", nil)
	if route != (proxy.Proxy{}) {
		store.Add(route)
	}
	p.proxy = newProxyView(store, nil, nil)
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

// TestPremiumMediaLeverIsForWhoHasPremiumOverWebSocket checks that the switch
// is on the Premium page of an account that has Premium and goes over Telegram's
// WebSocket, and of no other: it changes nothing anywhere else.
func TestPremiumMediaLeverIsForWhoHasPremiumOverWebSocket(t *testing.T) {
	socks := proxy.Proxy{Kind: proxy.SOCKS5, Host: "127.0.0.1", Port: 2080}
	own := proxy.Proxy{Kind: proxy.WebSocket, Host: "example.org", Port: 443}
	for name, c := range map[string]struct {
		premium model.PremiumSource
		route   proxy.Proxy
		shown   bool
	}{
		"Premium, Telegram's WebSocket":    {activePremium{}, proxy.Telegram(), true},
		"Premium, direct":                  {activePremium{}, proxy.Proxy{}, false},
		"Premium, SOCKS5":                  {activePremium{}, socks, false},
		"Premium, WebSocket of one's own":  {activePremium{}, own, false},
		"no Premium, Telegram's WebSocket": {plainPremium{}, proxy.Telegram(), false},
	} {
		with, without := premiumPageHeight(t, c.premium, true, c.route), premiumPageHeight(t, c.premium, false, c.route)
		if shown := with > without; shown != c.shown {
			t.Errorf("%s: %d high with the lever and %d without, shown %v, want %v", name, with, without, shown, c.shown)
		}
	}
}
