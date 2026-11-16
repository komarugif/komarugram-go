// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults/schemes"
	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestRenderProfiles saves the info of the demo's profiles, in a light
// theme and a dark one, for looking at them: a gift's cover (Анна), a
// profile color's (Новости Go), and a lone collectible name with the hint
// that points to it (Material Design), in Russian, and the last in English:
//
//	PROFILE_PNG_DIR=/tmp/profiles go test ./internal/messenger/ui -run RenderProfiles
func TestRenderProfiles(t *testing.T) {
	dir := os.Getenv("PROFILE_PNG_DIR")
	if dir == "" {
		t.Skip("set PROFILE_PNG_DIR to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		chat model.Chat
		lang string
	}{
		{"gift", model.Chat{ID: 2, Kind: model.KindUser, Title: "Анна Смирнова"}, "ru"},
		{"color", model.Chat{ID: 4, Kind: model.KindChannel, Title: "Новости Go"}, "ru"},
		{"collectible", model.Chat{ID: 8, Kind: model.KindChannel, Title: "Material Design"}, "ru"},
		{"collectible-en", model.Chat{ID: 8, Kind: model.KindChannel, Title: "Material Design"}, "en"},
	}
	for _, c := range cases {
		for _, dark := range []bool{false, true} {
			name := c.name
			if dark {
				name += "-dark"
			}
			renderProfile(t, filepath.Join(dir, "profile-"+name+".png"), c.chat, localization.For(c.lang), dark)
		}
	}
}

func renderProfile(t *testing.T, path string, chat model.Chat, l localization.Catalog, dark bool) {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	p.Open(chat)
	awaitShared(t, p)
	until := time.Now().Add(3 * time.Second)
	for p.profileResult != nil && time.Now().Before(until) {
		p.drainProfile()
		time.Sleep(time.Millisecond)
	}
	size := image.Pt(800, 1000)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	sc := schemes.SchemeBaselineLight()
	if dark {
		sc = schemes.SchemeBaselineDark()
	}
	ops := new(op.Ops)
	now := time.Now()
	var theme *token.Theme
	for range 30 {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: now, Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
		if theme == nil {
			theme = testTheme(gtx, sc)
		}
		wdk.InitMaterialThemeInContext(gtx, theme)
		fillRect(gtx, scheme(gtx).Surface.Color, size)
		images.BeginFrame()
		p.Layout(gtx, l, false)
		images.EndFrame()
		now = now.Add(50 * time.Millisecond)
	}
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
