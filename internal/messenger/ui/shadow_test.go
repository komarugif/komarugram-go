// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"testing"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestMakeShadow checks the picture drawShadow cuts into nine: darkest in
// the middle and next to the shape, fading to nothing at the edge of the
// blur, the same on every side, and as dark as the style says.
func TestMakeShadow(t *testing.T) {
	key := shadowKey{radius: 20, blur: 35, color: color.NRGBA{A: 107}}
	img := makeShadow(key)
	corner := key.radius + key.blur
	size := 2*corner + shadowMiddle
	if got := img.Bounds().Dx(); got != size {
		t.Fatalf("size = %d, want %d", got, size)
	}
	middle := size / 2
	center := img.RGBAAt(middle, middle).A
	if want := shadowColor(key.color, shadowLayers).A; center != want {
		t.Errorf("center alpha = %d, want %d", center, want)
	}
	if center >= key.color.A {
		t.Errorf("center alpha %d is not less than the style's %d: the layers cover each other with the rest", center, key.color.A)
	}
	if got := img.RGBAAt(0, middle).A; got > 1 {
		t.Errorf("edge alpha = %d, want none", got)
	}
	if got := img.RGBAAt(0, 0).A; got > 1 {
		t.Errorf("corner alpha = %d, want none", got)
	}
	// From the middle out the shadow only fades, equally on every side.
	last := center
	for x := middle; x >= 0; x-- {
		a := img.RGBAAt(x, middle).A
		if a > last {
			t.Fatalf("alpha rises from %d to %d at x=%d, going out", last, a, x)
		}
		last = a
		if got := img.RGBAAt(size-1-x, middle).A; got != a {
			t.Fatalf("alpha at x=%d is %d, but %d at its mirror", x, a, got)
		}
		if got := img.RGBAAt(middle, x).A; got != a {
			t.Fatalf("alpha at x=%d is %d across, but %d down", x, a, got)
		}
	}
	// Next to the shape it is darker than half way to the edge of the blur.
	if edge, far := img.RGBAAt(key.blur, middle).A, img.RGBAAt(key.blur/2, middle).A; edge <= far {
		t.Errorf("alpha next to the shape %d is not more than half way out, %d", edge, far)
	}
}

// TestDrawShadowSizes checks that shadows of shapes of every size are
// drawn, or left out when the shape is too small to cut the picture by,
// and that the picture is made once for all of them.
func TestDrawShadowSizes(t *testing.T) {
	gtx := layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	// Other tests have made pictures of their own.
	shadowPictures.Lock()
	clear(shadowPictures.made)
	shadowPictures.Unlock()
	for _, size := range []image.Point{{0, 0}, {10, 10}, {41, 200}, {42, 42}, {640, 740}, {5000, 3000}} {
		drawShadow(gtx, image.Rectangle{Max: size}, gtx.Dp(16), dialogShadow)
	}
	shadowPictures.Lock()
	made := len(shadowPictures.made)
	shadowPictures.Unlock()
	if made != 1 {
		t.Errorf("%d pictures made for one style, want 1", made)
	}
}

// TestShadowIntensity checks that the setting scales the shadow: none at 0,
// and darker pictures as it grows.
func TestShadowIntensity(t *testing.T) {
	gtx := layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	card := image.Rect(0, 0, 400, 300)
	made := func() map[shadowKey]bool {
		shadowPictures.Lock()
		defer shadowPictures.Unlock()
		keys := map[shadowKey]bool{}
		for key := range shadowPictures.made {
			keys[key] = true
		}
		return keys
	}
	alphas := map[int]uint8{}
	for _, percent := range []int{0, 50, 100, 200} {
		shadowPictures.Lock()
		clear(shadowPictures.made)
		shadowPictures.Unlock()
		withShadowIntensity(gtx, percent)
		drawShadow(gtx, card, 16, dialogShadow)
		keys := made()
		if percent == 0 {
			if len(keys) != 0 {
				t.Errorf("a shadow at 0%% made %d pictures", len(keys))
			}
			continue
		}
		for key := range keys {
			alphas[percent] = key.color.A
		}
	}
	if !(alphas[50] < alphas[100] && alphas[100] < alphas[200]) {
		t.Errorf("alphas at 50, 100 and 200%%: %d, %d, %d; want them to grow", alphas[50], alphas[100], alphas[200])
	}
	// A frame with no setting has the full shadow.
	gtx.Values = map[string]any{}
	if got := shadowIntensity(gtx); got != 1 {
		t.Errorf("intensity without a setting = %v, want 1", got)
	}
}
