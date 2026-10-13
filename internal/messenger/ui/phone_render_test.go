// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/preferences"
)

// TestRenderPhoneStep draws the phone number step of the sign-in, empty,
// with a number being typed, and with the list of countries open, and saves
// them, for looking at them:
//
//	PHONE_PNG_DIR=/some/dir go test ./internal/messenger/ui -run RenderPhoneStep
func TestRenderPhoneStep(t *testing.T) {
	dir := os.Getenv("PHONE_PNG_DIR")
	if dir == "" {
		t.Skip("set PHONE_PNG_DIR to a directory")
	}
	// The fonts named by KOMARUGRAM_FONT_EMOJI and the like are the ones drawn with.
	applyFonts(preferences.Fonts{}, nil)
	t.Cleanup(func() { applyFonts(preferences.Fonts{}, nil) })
	size := image.Pt(900, 620)
	for _, lang := range []string{"ru", "en"} {
		l := localization.For(lang)
		for _, scene := range []struct {
			name   string
			number string
			chosen string
			search string
			open   bool
		}{
			{name: "empty"},
			{name: "typing", number: "79001"},
			{name: "russia", number: "79001234567"},
			{name: "usa", number: "12345678910"},
			{name: "canada", number: "14165550123"},
			{name: "kazakhstan", number: "77011234567"},
			{name: "unknown", number: "999123"},
			{name: "picked", chosen: "KZ", number: "7"},
			{name: "list", open: true},
			{name: "list-search", open: true, search: "ge"},
			{name: "list-code", open: true, search: "+38"},
			{name: "list-none", open: true, search: "zzz"},
		} {
			signIn := login.New(nil)
			ctx, cancel := context.WithCancel(context.Background())
			go signIn.Phone(ctx, nil)
			for signIn.State().Step != login.StepPhone {
				time.Sleep(time.Millisecond)
			}
			page := newLoginPage(signIn, newSecurityView(nil, func() {}), nil)
			page.phone.chosen = scene.chosen
			page.phone.editor.SetText(scene.number)
			opened := false
			frames := 4
			if scene.open {
				// The dialog animates in, in real time.
				frames = 60
			}
			renderPNG(t, filepath.Join(dir, lang+"-"+scene.name+".png"), size, frames, func(gtx layout.Context) {
				if scene.open {
					time.Sleep(10 * time.Millisecond)
				}
				if scene.open && !opened {
					opened = true
					page.phone.picker.Open(gtx)
					page.phone.picker.search.SetText(scene.search)
				}
				page.Update(gtx, l)
				page.Layout(gtx, l, false)
			})
			cancel()
		}
	}
}
