// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/chattheme"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

func TestChatLookComesFromTheChatThenTheSettings(t *testing.T) {
	own := &model.ChatThemeStyle{Incoming: 0x112233ff, Wallpaper: &model.ChatWallpaper{Colors: []uint32{1}}}
	peer := &model.ChatWallpaper{Colors: []uint32{2}}
	classic := preferences.ChatMode{Theme: "classic"}
	custom := preferences.ChatMode{Theme: "classic", Wallpaper: &preferences.Wallpaper{Colors: []uint32{3}}}
	cases := []struct {
		name      string
		a         model.ChatAppearance
		mode      preferences.ChatMode
		incoming  uint32
		wallpaper uint32
	}{
		{"chat's theme", model.ChatAppearance{Theme: model.ChatTheme{Light: own}}, custom, 0x112233ff, 1},
		{"chat's wallpaper", model.ChatAppearance{Theme: model.ChatTheme{Light: own}, Wallpaper: peer}, custom, 0x112233ff, 2},
		{"settings' theme", model.ChatAppearance{}, classic, 0xffffffff, 0xdbddbb},
		// The settings' own wallpaper is read by the job: none here.
		{"settings' wallpaper", model.ChatAppearance{}, custom, 0xffffffff, 0},
	}
	for _, c := range cases {
		style, w := resolveLook(c.a, false, c.mode)
		if style == nil || style.Incoming != c.incoming {
			t.Fatalf("%s: style %+v", c.name, style)
		}
		got := uint32(0)
		if w != nil {
			got = w.Colors[0]
		}
		if got != c.wallpaper {
			t.Fatalf("%s: wallpaper %v", c.name, got)
		}
	}
	if style, w := resolveLook(model.ChatAppearance{}, false, preferences.ChatMode{}); style != nil || w != nil {
		t.Fatal("the application's colors have a look", style, w)
	}
	// A dark chat takes the theme's dark variant.
	dark := &model.ChatThemeStyle{Dark: true}
	if style, _ := resolveLook(model.ChatAppearance{Theme: model.ChatTheme{Light: own, Dark: dark}}, true, classic); style != dark {
		t.Fatal("dark variant")
	}
}

func TestSettingsChangeTheLookOfChats(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	themes := newChatThemeController(store, &images, func() {})
	defer themes.Close()
	mode := preferences.ChatMode{}
	themes.look = func(bool) preferences.ChatMode { return mode }
	// Chat 3 has no theme of its own.
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for themes.loading || themes.rendering != nil {
			if time.Now().After(deadline) {
				t.Fatal("the look did not come")
			}
			time.Sleep(5 * time.Millisecond)
			themes.Update(3, false)
		}
	}
	themes.Update(3, false)
	wait()
	if themes.style != nil || themes.background != nil {
		t.Fatal("the application's colors have a look")
	}
	mode = preferences.ChatMode{Theme: string(chattheme.PresetDay)}
	themes.Update(3, false)
	wait()
	if themes.style == nil || themes.style.Outgoing[0] != 0xdef1fd || themes.background == nil {
		t.Fatal("Day was not taken", themes.style)
	}
	// The wallpaper is rendered at the size the history takes.
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(image.Pt(640, 480)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	themes.Background(gtx)
	wait()
	if themes.image == nil || themes.image.Bounds().Size() != image.Pt(640, 480) {
		t.Fatal("wallpaper size", themes.size)
	}
	// Messages share one themed context in a frame.
	a := themes.messageContext(themes.historyContext(gtx), true)
	b := themes.messageContext(themes.historyContext(gtx), true)
	if len(themes.contexts) != 2 || scheme(a) != scheme(b) {
		t.Fatal("contexts made again", len(themes.contexts))
	}
}
