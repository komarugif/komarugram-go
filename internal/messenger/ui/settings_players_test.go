// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp/appearance"
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

// TestSettingsOpenDoesNotWaitForPlayers opens the settings while the
// players are still being looked for: finding the browser of Mini Apps
// starts programs, and took up to a second of the first frame. The page
// draws at once, says it is looking, and names the player once it is found.
func TestSettingsOpenDoesNotWaitForPlayers(t *testing.T) {
	l := localization.For("ru")
	p := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, nil,
		func() string { return "" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
	release := make(chan struct{})
	p.players.find = func(map[player.Kind]string) []player.Kind {
		<-release
		return []player.Kind{player.VLC}
	}
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Constraints: layout.Exact(image.Pt(800, 1600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	frame := func() {
		ops.Reset()
		gtx.Now = time.Now()
		p.Update(gtx, themeAuto, "ru")
		p.Layout(gtx, themeAuto, appearance.Light, false, l)
	}

	drawn := make(chan struct{})
	go func() {
		p.open()
		frame()
		close(drawn)
	}()
	select {
	case <-drawn:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the settings waited for the players to be found")
	}
	if got := p.players.subtitle(l); !strings.Contains(got, l.T("program.searching")) {
		t.Errorf("subtitle while looking: %q", got)
	}

	close(release)
	for deadline := time.Now().Add(5 * time.Second); !p.players.checked; {
		if time.Now().After(deadline) {
			t.Fatal("the players found never came")
		}
		time.Sleep(10 * time.Millisecond)
		frame()
	}
	if got := p.players.subtitle(l); !strings.Contains(got, "VLC") {
		t.Errorf("subtitle once found: %q", got)
	}
}
