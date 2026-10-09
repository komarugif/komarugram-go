// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"
	"testing"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
	"komarugram/pkg/player"
)

// Where mpv does not run, as on Windows 7, it is neither offered nor
// named: not in the choice of the player, not in the integrations, not in
// the texts.
func TestPlayersWithoutMPV(t *testing.T) {
	saved := player.Kinds
	player.Kinds = []player.Kind{player.VLC, player.Chromium}
	t.Cleanup(func() { player.Kinds = saved })

	for _, lang := range []string{"ru", "en"} {
		l := localization.For(lang)
		for _, key := range []string{"player.none", "player.fallback", "player.browser_cannot_play", "sticker_player.external"} {
			if text := playerText(l, key); strings.Contains(strings.ToLower(text), "mpv") || text == key+"_no_mpv" {
				t.Errorf("%s %s: %q", lang, key, text)
			}
		}
	}
	for _, lang := range []string{"ru", "en"} {
		if text := localization.For(lang).Format("player.only_no_mpv", map[string]string{"player": "VLC"}); !strings.Contains(text, "VLC") {
			t.Errorf("%s: %q", lang, text)
		}
	}
	s := newPlayerSettings()
	if s.programs[player.MPV] != nil {
		t.Error("mpv's program is offered")
	}
	p := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, nil,
		func() string { return "" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(800, 3000)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	p.section = settingsIntegrations
	p.Update(gtx, themeAuto, "ru")
	p.layoutIntegrations(gtx, localization.For("ru"))
}
