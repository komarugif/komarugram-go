// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/security"
)

// protectPage is the sign-in at the offer to protect local data, over a
// manager that has no TPM, whose offer has the most to say.
func protectPage(t *testing.T) *loginPage {
	t.Helper()
	protection, err := security.OpenPath(filepath.Join(t.TempDir(), "security.json"), &renderTPM{access: security.Access{Kind: security.AccessMissing}})
	if err != nil {
		t.Fatal(err)
	}
	signIn := login.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go signIn.OfferProtection(ctx)
	for signIn.State().Step != login.StepProtect {
		time.Sleep(time.Millisecond)
	}
	return newLoginPage(signIn, newSecurityView(protection, func() {}), nil)
}

// TestProtectOfferFitsALowWindow checks that the card of the offer is no
// higher than the window: what does not fit scrolls, with the title and the
// buttons in sight.
func TestProtectOfferFitsALowWindow(t *testing.T) {
	l := localization.For("ru")
	for _, height := range []int{300, 420, 600} {
		page := protectPage(t)
		ops := new(op.Ops)
		for range 3 {
			ops.Reset()
			gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(image.Pt(900, height)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
			page.Update(gtx, l)
			page.Layout(gtx, l, false)
		}
		// The card has the window's margin, 16 dp, around it.
		if card := page.height.value; card > height-2*16 {
			t.Errorf("a window %d high: the card is %d high, more than the window has room for", height, card)
		}
		if page.height.value <= 0 {
			t.Errorf("a window %d high: no card", height)
		}
	}
}

// TestRenderProtectLow draws the offer in a window too low for it, scrolled
// to the top and to the end, in ACCOUNTS_PNG_DIR.
func TestRenderProtectLow(t *testing.T) {
	dir := os.Getenv("ACCOUNTS_PNG_DIR")
	if dir == "" {
		t.Skip("set ACCOUNTS_PNG_DIR to a directory")
	}
	l := localization.For("ru")
	for name, scrolled := range map[string]bool{"protect-low": false, "protect-low-end": true} {
		page := protectPage(t)
		frame := 0
		renderPNG(t, filepath.Join(dir, name+".png"), image.Pt(900, 420), 6, func(gtx layout.Context) {
			frame++
			if scrolled && frame == 3 {
				page.protect.List.Position.First = 0
				page.protect.List.Position.Offset = 1 << 20
			}
			page.Update(gtx, l)
			page.Layout(gtx, l, false)
		})
	}
}

// imageOf draws one frame of draw, in a window of size, at 1 pixel to a dp.
func imageOf(t *testing.T, size image.Point, frames int, draw func(gtx layout.Context)) *image.RGBA {
	t.Helper()
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := new(op.Ops)
	for range frames {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		draw(gtx)
	}
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	return img
}

// TestProtectFieldsKeepTheirEdges checks that the fields of the offer, which
// scroll, are drawn whole: the outline of a field lies on its edge, and a
// list that clips at the edge of the field takes half of it. The field is
// drawn alone, with a margin, and looked for in the page.
func TestProtectFieldsKeepTheirEdges(t *testing.T) {
	l := localization.For("ru")
	page := protectPage(t)
	const margin = 4
	// The card is 420 dp wide, in the middle of the window, and has the
	// padding of a card; the field is as wide as what is inside it.
	pageSize := image.Pt(900, 900)
	cardX := (pageSize.X - 420) / 2
	fieldX := cardX + int(defaultCardPadding)
	fieldWidth := 420 - 2*int(defaultCardPadding)

	view := imageOf(t, pageSize, 3, func(gtx layout.Context) {
		fillRect(gtx, scheme(gtx).SurfaceContainerLow, gtx.Constraints.Max)
		page.Update(gtx, l)
		page.Layout(gtx, l, false)
	})
	var fieldSize image.Point
	field := imageOf(t, image.Pt(fieldWidth+2*margin, 120), 1, func(gtx layout.Context) {
		fillRect(gtx, scheme(gtx).Surface.Color, gtx.Constraints.Max)
		offset(gtx, image.Pt(margin, margin), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = fieldWidth
			gtx.Constraints.Min = image.Pt(fieldWidth, 0)
			dims := page.security.master.Layout(gtx, l.T("security.password"), false)
			fieldSize = dims.Size
			return dims
		})
	})
	height := fieldSize.Y + 2*margin
	width := fieldSize.X + 2*margin

	// Where the field is looked for: down the page, a pixel either side of
	// where it should be. The least difference there is, over the field's
	// pixels, is what the field is, as it is in the page.
	best := -1
	for dx := -1; dx <= 1; dx++ {
		for y := 0; y+height <= pageSize.Y; y++ {
			diff := 0
			for row := 0; row < height && (best < 0 || diff < best); row++ {
				a := field.Pix[row*field.Stride : row*field.Stride+4*width]
				at := (y+row)*view.Stride + 4*(fieldX-margin+dx)
				b := view.Pix[at : at+4*width]
				for i := range a {
					d := int(a[i]) - int(b[i])
					if d < 0 {
						d = -d
					}
					diff += d
				}
			}
			if best < 0 || diff < best {
				best = diff
			}
		}
	}
	t.Logf("the field differs by %d from the one drawn alone (%d x %d pixels)", best, width, height)
	// An edge cut in half differs by tens of thousands; the rest is rounding.
	if best > 2000 {
		t.Errorf("the field is not in the page as it is drawn alone: its pixels differ by %d in all", best)
	}
}

// TestRenderProtectStrength draws the offer with passwords of each level
// typed, in ACCOUNTS_PNG_DIR.
func TestRenderProtectStrength(t *testing.T) {
	dir := os.Getenv("ACCOUNTS_PNG_DIR")
	if dir == "" {
		t.Skip("set ACCOUNTS_PNG_DIR to a directory")
	}
	l := localization.For("ru")
	for name, password := range map[string]string{
		"protect-strength-none": "", "protect-strength-0": "password1", "protect-strength-1": "Anna1990",
		"protect-strength-2": "vkqzmw", "protect-strength-3": "vkqzmwpd", "protect-strength-4": "x7#kQ9$mP2vL",
	} {
		page := protectPage(t)
		page.security.master.editor.SetText(password)
		renderPNG(t, filepath.Join(dir, name+".png"), image.Pt(900, 820), 4, func(gtx layout.Context) {
			page.Update(gtx, l)
			page.Layout(gtx, l, false)
		})
	}
}

// TestRenderSettingsSecurity draws the card of the settings in which local
// data is protected later, with a password typed, and with its eye held
// down, in ACCOUNTS_PNG_DIR.
func TestRenderSettingsSecurity(t *testing.T) {
	dir := os.Getenv("ACCOUNTS_PNG_DIR")
	if dir == "" {
		t.Skip("set ACCOUNTS_PNG_DIR to a directory")
	}
	l := localization.For("ru")
	for name, held := range map[string]bool{"settings-security": false, "settings-security-peek": true} {
		view := protectPage(t).security
		view.master.editor.SetText("Xq7vkmzp#2")
		view.confirm.editor.SetText("Xq7vkm")
		view.master.peek.down = held
		renderPNG(t, filepath.Join(dir, name+".png"), image.Pt(700, 640), 4, func(gtx layout.Context) {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(560))
			layout.Inset{Top: 20, Left: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return card(gtx, func(gtx layout.Context) layout.Dimensions { return view.SettingsLayout(gtx, l) }, defaultCardPadding)
			})
		})
	}
}
