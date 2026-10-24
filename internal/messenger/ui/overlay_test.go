// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/exp/powersave"
	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// pixelAfter draws an overlay of 100x50 over a red page of 200x100, with
// the blurred page behind it when backdrop, and returns a pixel in the middle
// of the overlay.
func pixelAfter(t *testing.T, opacity float32, blurred bool) color.NRGBA {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overlay.png")
	renderFrames(t, image.Pt(200, 100), path, func(gtx layout.Context) {
		macro := op.Record(gtx.Ops)
		paint.FillShape(gtx.Ops, color.NRGBA{R: 255, A: 255}, clip.Rect{Max: image.Pt(200, 100)}.Op())
		// The page is only recorded, not drawn: the overlay is the only
		// thing that can show it.
		page := macro.Stop()
		var bd *blurBackdrop
		if blurred {
			bd = newBackdrop(page, opacity)
		}
		origin := image.Pt(50, 25)
		defer op.Offset(origin).Push(gtx.Ops).Pop()
		overlayPlate(gtx, bd, image.Pt(100, 50), origin, token.NewMatColorFromHexRGB(0xffffff), 0)
	})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return color.NRGBAModel.Convert(img.At(100, 50)).(color.NRGBA)
}

// An overlay without a backdrop is its fill; with one, the fill is translucent
// over the page behind it as opaque as the preferences say, and the page
// behind is where the overlay is, whatever it is laid out at.
func TestOverlayFillShowsThePageBehind(t *testing.T) {
	opaque := pixelAfter(t, 0.5, false)
	if opaque.G != 255 || opaque.B != 255 {
		t.Fatalf("an overlay without a backdrop is %+v, want white", opaque)
	}
	half := pixelAfter(t, 0.5, true)
	// Blending is in linear light, which makes half of white over red a
	// pink well above the middle of the two in sRGB.
	if half.G < 120 || half.G > 230 || half.R < 250 {
		t.Fatalf("a half-opaque overlay over red is %+v, want a pink between white and red", half)
	}
	mostly := pixelAfter(t, 0.9, true)
	if mostly.G <= half.G || mostly.G >= 255 {
		t.Fatalf("a 90%% opaque overlay is %+v, want whiter than the half-opaque one %+v, and not white", mostly, half)
	}
}

// The page records what is behind its overlays only for the ones that blur.
func TestPageRecordsBackdropForBlurringOverlays(t *testing.T) {
	for _, tc := range []struct {
		name           string
		prefs          *overlayPrefs
		composerBlur   bool
		recorded       bool
		menus, toasts_ bool
	}{
		{"none", &overlayPrefs{opacity: 0.6}, false, false, false, false},
		{"menus", &overlayPrefs{menus: true, opacity: 0.6}, false, true, true, false},
		{"toasts", &overlayPrefs{toasts: true, opacity: 0.6}, false, true, false, true},
		{"the composer", &overlayPrefs{opacity: 0.6}, true, true, false, false},
		{"all", &overlayPrefs{menus: true, toasts: true, opacity: 0.6}, true, true, true, true},
	} {
		h := newMenuHarness(t, nil)
		p := h.page
		p.overlays = func() overlayPrefs { return *tc.prefs }
		p.classic = func() bool { return false }
		p.blur = func() bool { return tc.composerBlur }
		h.frames(3)
		if (p.bd != nil) != tc.recorded {
			t.Errorf("%s: recorded %t, want %t", tc.name, p.bd != nil, tc.recorded)
		}
		if (p.menuBackdrop() != nil) != tc.menus || (p.toastBackdrop() != nil) != tc.toasts_ {
			t.Errorf("%s: menus %t toasts %t, want %t %t", tc.name, p.menuBackdrop() != nil, p.toastBackdrop() != nil, tc.menus, tc.toasts_)
		}
		if (p.toast.bd != nil) != tc.toasts_ {
			t.Errorf("%s: the toast blurs %t, want %t", tc.name, p.toast.bd != nil, tc.toasts_)
		}
		if p.bd != nil && float32(p.bd.opacity) != 0.6 {
			t.Errorf("%s: opacity %v, want the preference's", tc.name, p.bd.opacity)
		}
	}
}

// The list records its rows for the menu and the toast that blur them.
func TestListRecordsBackdropForBlurringOverlays(t *testing.T) {
	h := newListHarness(t, testChats(1))
	h.frames(2)
	if h.list.bd != nil {
		t.Fatal("the rows were recorded although nothing blurs them")
	}
	h.list.overlays = func() overlayPrefs { return overlayPrefs{menus: true, opacity: 0.5} }
	h.frames(2)
	if h.list.bd == nil || h.list.menuBackdrop() == nil {
		t.Fatal("the rows were not recorded for a menu that blurs them")
	}
	// The list's toast shows in the chat or over the window, not over the
	// rows: toasts that blur leave them unrecorded.
	h.list.overlays = func() overlayPrefs { return overlayPrefs{toasts: true, opacity: 0.5} }
	h.list.toast.Show("hello")
	h.frames(2)
	if h.list.bd != nil || h.list.menuBackdrop() != nil {
		t.Fatalf("toasts only: recorded %t, menus blur %t", h.list.bd != nil, h.list.menuBackdrop() != nil)
	}
	// The menu of a chat opens and draws over the blurred rows.
	h.list.overlays = func() overlayPrefs { return overlayPrefs{menus: true, toasts: true, opacity: 0.5} }
	h.rightClick(3)
	h.frames(5)
	if !h.list.menu.open || h.list.menuBackdrop() == nil {
		t.Fatal("the menu did not open over a backdrop")
	}
}

// The window turns the preferences into how overlays are drawn: they blur
// while animations are on, with the opacity the transparency leaves.
func TestAppOverlayPrefs(t *testing.T) {
	for _, tc := range []struct {
		mode  powersave.Mode
		blurs bool
	}{{powersave.ModeOn, true}, {powersave.ModeOff, false}} {
		w := &appwindow.Window{Motion: motion.New(func() {})}
		store := preferences.Memory()
		if err := store.SetMotion(tc.mode, powersave.DefaultLowBattery); err != nil {
			t.Fatal(err)
		}
		if err := store.SetOverlays(preferences.Overlays{Transparency: 40, MenusBlur: true}); err != nil {
			t.Fatal(err)
		}
		a := New(w, mockstore.New(time.Now(), 0), Services{MiniApps: miniappprefs.New(miniapp.Shared), Preferences: store})
		got := a.overlayPrefs()
		if got.menus != tc.blurs || got.toasts || got.opacity != 0.6 {
			t.Errorf("animations %v: %+v, want menus blur %t, toasts not, opacity 0.6", tc.mode, got, tc.blurs)
		}
		w.Motion.Close()
	}
}

// However transparent the overlays are, a toast keeps enough of its dark
// plate for its light text; up to that it follows them.
func TestToastKeepsItsPlate(t *testing.T) {
	var tst toast
	if tst.backdrop() != nil {
		t.Fatal("a toast without a backdrop blurs")
	}
	tst.bd = newBackdrop(op.CallOp{}, 0.3)
	if got := tst.backdrop().opacity; got != toastMinOpacity {
		t.Fatalf("at 70%% transparency the toast is %v opaque, want %v", got, toastMinOpacity)
	}
	if tst.bd.opacity != 0.3 {
		t.Fatal("the page's backdrop was changed")
	}
	tst.bd = newBackdrop(op.CallOp{}, 0.9)
	if got := tst.backdrop().opacity; got != 0.9 {
		t.Fatalf("at 10%% transparency the toast is %v opaque, want 0.9", got)
	}
}
