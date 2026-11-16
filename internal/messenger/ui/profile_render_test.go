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
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// TestRenderProfiles saves the info of the demo's profiles, in a light
// theme and a dark one, for looking at them: a gift's cover (Анна), a
// profile color's (Новости Go), and a lone collectible name with the hint
// that points to it (Material Design), in Russian, and the last in English;
// then the page of a profile's QR code (Анна), with another background in
// the dark theme:
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
	for _, dark := range []bool{false, true} {
		name := "profile-qr.png"
		if dark {
			name = "profile-qr-dark.png"
		}
		renderProfileQR(t, filepath.Join(dir, name), cases[0].chat, localization.For("ru"), dark)
	}
	// The account's own, as its page: as it is, in a dark theme, covered by
	// visual privacy, being edited, and its playlist, in a window as wide as
	// the page right of the chat list.
	for _, own := range []struct {
		name                   string
		dark, private, editing bool
		section                model.SharedKind
	}{
		{"own", false, false, false, ""}, {"own-dark", true, false, false, ""}, {"own-private", false, true, false, ""},
		{"own-edit", false, false, true, ""}, {"own-playlist", false, false, false, model.SharedSavedMusic},
	} {
		renderOwnProfile(t, filepath.Join(dir, "profile-"+own.name+".png"), own.dark, own.private, own.editing, own.section)
	}
}

func renderOwnProfile(t *testing.T, path string, dark, private, editing bool, section model.SharedKind) {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	p := newOwnProfile(t, store, func() {})
	me := store.Me()
	p.open(me)
	awaitShared(t, p.info)
	until := time.Now().Add(3 * time.Second)
	for p.info.profileResult != nil && time.Now().Before(until) {
		p.info.drainProfile()
		time.Sleep(time.Millisecond)
	}
	if section != "" {
		p.info.selectSection(section)
		awaitShared(t, p.info)
	}
	if editing {
		p.editing = true
		p.first.editor.SetText(me.FirstName)
		p.last.editor.SetText(me.LastName)
		p.username.editor.SetText(me.Username)
		p.bio.editor.SetText(me.Bio)
	}
	l := localization.For("ru")
	renderInfoFrames(t, path, image.Pt(1000, 1250), dark, p.info.renderer.images, func(gtx layout.Context) {
		p.Update(gtx, me, func() {})
		p.Layout(gtx, me, l, private, false)
	})
}

func renderProfile(t *testing.T, path string, chat model.Chat, l localization.Catalog, dark bool) {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	// Avatars are plain circles, to compare the pictures of the cards with.
	p.drawAvatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
		side := image.Pt(gtx.Dp(size), gtx.Dp(size))
		paint.FillShape(gtx.Ops, scheme(gtx).Tertiary.Color.AsNRGBA(), clip.Ellipse{Max: side}.Op(gtx.Ops))
		return layout.Dimensions{Size: side}
	}
	p.Open(chat)
	awaitShared(t, p)
	until := time.Now().Add(3 * time.Second)
	for p.profileResult != nil && time.Now().Before(until) {
		p.drainProfile()
		time.Sleep(time.Millisecond)
	}
	renderInfoFrames(t, path, image.Pt(800, 1000), dark, &images, func(gtx layout.Context) {
		fillRect(gtx, scheme(gtx).Surface.Color, gtx.Constraints.Max)
		p.Layout(gtx, l, false)
	})
}

// renderProfileQR saves the page of the QR code of chat's info, opened by
// the button in the info's header; a dark theme has the fifth background.
func renderProfileQR(t *testing.T, path string, chat model.Chat, l localization.Catalog, dark bool) {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	p.drawAvatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
		side := image.Pt(gtx.Dp(size), gtx.Dp(size))
		paint.FillShape(gtx.Ops, scheme(gtx).Tertiary.Color.AsNRGBA(), clip.Ellipse{Max: side}.Op(gtx.Ops))
		return layout.Dimensions{Size: side}
	}
	p.Open(chat)
	awaitShared(t, p)
	p.OpenQR()
	if !p.qr.shown {
		t.Fatalf("the info of %q has no QR code", chat.Title)
	}
	if dark {
		p.qr.choice = 4
	}
	renderInfoFrames(t, path, image.Pt(800, 1000), dark, &images, func(gtx layout.Context) {
		fillRect(gtx, scheme(gtx).Surface.Color, gtx.Constraints.Max)
		p.Layout(gtx, l, false)
	})
}

// renderInfoFrames draws 30 frames of draw, 50 ms apart, and saves the last
// at path.
func renderInfoFrames(t *testing.T, path string, size image.Point, dark bool, images *imageOps, draw func(gtx layout.Context)) {
	t.Helper()
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
		images.BeginFrame()
		draw(gtx)
		images.EndFrame()
		now = now.Add(50 * time.Millisecond)
		// What loads in the background, as a song's cover, comes.
		time.Sleep(10 * time.Millisecond)
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
