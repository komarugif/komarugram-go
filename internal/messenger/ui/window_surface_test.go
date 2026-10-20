// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/exp/powersave"
	"gio-mw/wdk"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/miniappprefs"
	"komarugram/pkg/miniapp"
)

// Start transparent, as a Wayland buffer does, and check the actual main
// window pixels. This catches an opaque root under the panels, double-painted
// headers, and opacity accidentally applied to the contents of a panel.
func TestWindowSurfacePixels(t *testing.T) {
	t.Chdir("../../..") // The demo media paths are relative to the repository root.
	size := image.Pt(1200, 760)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skip("no GPU for headless rendering:", err)
	}
	defer win.Release()
	for _, tc := range []struct {
		name         string
		transparency int
		supported    bool
		mode         string
	}{
		{"opaque", 0, true, ""},
		{"translucent", 40, true, ""},
		{"maximum", preferences.TransparencyMax, true, ""},
		{"unsupported", 40, false, ""},
		{"search", 40, true, "search"},
		{"selection", 40, true, "selection"},
		{"classic", 40, true, "classic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefs := preferences.Memory()
			if err := prefs.SetTheme(preferences.ThemeLight); err != nil {
				t.Fatal(err)
			}
			if err := prefs.SetWindowTransparency(tc.transparency); err != nil {
				t.Fatal(err)
			}
			if tc.mode == "classic" {
				if err := prefs.SetComposer(preferences.ComposerClassic); err != nil {
					t.Fatal(err)
				}
			}
			a := New(testWindow(t), mockstore.New(time.Unix(1000, 0), 0), Services{
				Preferences: prefs, MiniApps: miniappprefs.New(miniapp.Ephemeral),
			})
			t.Cleanup(a.Close)
			a.selected = 2
			// Initials have no asynchronous image load and are still opaque media.
			a.chats.avatar = avatar
			ops := new(op.Ops)
			for i := range 3 {
				ops.Reset()
				gtx := layout.Context{Ops: ops, Now: time.Unix(1000, 0).Add(time.Duration(i) * time.Second), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
				wdk.InitMaterialThemeInContext(gtx, a.Theme(gtx))
				wdk.SetAnimationsEnabled(gtx, false)
				a.Update(gtx)
				if i > 0 {
					a.history.chatSearch.open = tc.mode == "search"
					if tc.mode == "selection" {
						a.history.selection.selected = map[model.MessageID]bool{a.history.messages[0].Key.MessageID: true}
					}
				}
				a.layoutWindow(gtx, tc.supported)
			}
			if err := win.Frame(ops); err != nil {
				t.Fatal(err)
			}
			img := image.NewRGBA(image.Rectangle{Max: size})
			if err := win.Screenshot(img); err != nil {
				t.Fatal(err)
			}
			want := 255
			if tc.supported {
				want = (100 - tc.transparency) * 255 / 100
			}
			for name, at := range map[string]image.Point{
				"sidebar": {2, 300}, "chat list": {86, 300}, "header": {700, 2},
			} {
				if got := int(img.RGBAAt(at.X, at.Y).A); got < want-1 || got > want+1 {
					t.Errorf("%s alpha %d, want %d", name, got, want)
				}
			}
			if got := img.RGBAAt(700, 500).A; got != 255 {
				t.Errorf("history alpha %d, want opaque", got)
			}
			// The bottom edge of the chat: the classic composer's bar, a
			// surface of the window; under a floating one, the history.
			bottom := int(img.RGBAAt(700, size.Y-3).A)
			if tc.mode == "classic" {
				if bottom < want-1 || bottom > want+1 {
					t.Errorf("classic composer alpha %d, want %d", bottom, want)
				}
			} else if bottom != 255 {
				t.Errorf("history under the floating composer alpha %d, want opaque", bottom)
			}
			if got := img.RGBAAt(115, 26).A; got != 255 {
				t.Errorf("chat avatar alpha %d, want opaque", got)
			}
			if tc.mode == "" {
				// Text cores stay opaque; anti-aliased edges retain their coverage.
				opaque := 0
				for y := 8; y < 28; y++ {
					for x := 480; x < 620; x++ {
						if p := img.RGBAAt(x, y); p.A == 255 && p.R < 100 && p.G < 100 && p.B < 100 {
							opaque++
						}
					}
				}
				if opaque < 20 {
					t.Errorf("only %d opaque text pixels in header", opaque)
				}
			}
			if dir := os.Getenv("WINDOW_SURFACES_PNG_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				f, err := os.Create(filepath.Join(dir, "window-surfaces-"+tc.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, windowSurfacePNG(img)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// Gio's headless target stores sRGB-encoded, linear-premultiplied pixels.
// PNG needs straight sRGB channels: unpremultiply in linear light, as Gio's
// own render tests do, rather than letting image.RGBA unpremultiply in sRGB.
func windowSurfacePNG(src *image.RGBA) *image.NRGBA {
	var linear [256]float64
	for i := range linear {
		c := float64(i) / 255
		if c <= 0.04045 {
			linear[i] = c / 12.92
		} else {
			linear[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	channel := func(c, alpha uint8) uint8 {
		v := linear[c] / (float64(alpha) / 255)
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		return uint8(min(255, max(0, math.Round(v*255))))
	}
	dst := image.NewNRGBA(src.Bounds())
	for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
		for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
			c := src.RGBAAt(x, y)
			out := color.NRGBA{A: c.A}
			if c.A != 0 {
				out.R, out.G, out.B = channel(c.R, c.A), channel(c.G, c.A), channel(c.B, c.A)
			}
			dst.SetNRGBA(x, y, out)
		}
	}
	return dst
}

func TestWindowBlurRequestIndependent(t *testing.T) {
	prefs := preferences.Memory()
	if err := prefs.SetMotion(powersave.ModeOff, 20); err != nil {
		t.Fatal(err)
	}
	a := New(testWindow(t), mockstore.New(time.Unix(1000, 0), 0), Services{Preferences: prefs, MiniApps: miniappprefs.New(miniapp.Ephemeral)})
	defer a.Close()
	for _, tc := range []struct {
		transparency int
		blur, want   bool
	}{
		{40, true, true}, {40, false, false}, {40, true, true}, {0, true, false},
	} {
		if err := prefs.SetWindowTransparency(tc.transparency); err != nil {
			t.Fatal(err)
		}
		if err := prefs.SetWindowBlur(tc.blur); err != nil {
			t.Fatal(err)
		}
		a.updateWindowEffects()
		if a.windowBlurWanted != tc.want {
			t.Errorf("transparency %d, blur %t: requested %t", tc.transparency, tc.blur, a.windowBlurWanted)
		}
	}
}
