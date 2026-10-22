// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// A transparent window starts every frame transparent, as the headless
// renderer does. What the viewer paints then is what the compositor blends:
// the backdrop with its alpha, premultiplied, and the photo opaque.
func TestTranslucentViewerPixels(t *testing.T) {
	size := image.Pt(800, 600)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skip("no GPU for headless rendering:", err)
	}
	defer win.Release()
	h := newViewerHarness(t)
	s := h.store
	h.viewer.standalone = true
	h.viewer.backdropColor = viewerBackdropBlurred
	h.viewer.Open(1, s.photos[14], s.photos)
	h.until("the photo", func() bool {
		return h.viewer.full.StatusFit(s.photos[14], false, image.Pt(656, 460), false).Frame != nil
	})
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineDark()))
	h.images.BeginFrame()
	h.viewer.Layout(gtx, localization.For("en"), false)
	h.images.EndFrame()
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	// The stage is x 72…728, y 56…516; the 1600×1200 photo fits 613×460 in
	// its middle. Between the side zone and the photo is plain backdrop.
	bg := img.RGBAAt(80, 300)
	if d := int(bg.A) - int(viewerBackdropBlurred.A); d < -2 || d > 2 {
		t.Errorf("backdrop alpha %d, want %d", bg.A, viewerBackdropBlurred.A)
	}
	if bg.R > bg.A || bg.G > bg.A || bg.B > bg.A {
		t.Errorf("backdrop %v is not premultiplied", bg)
	}
	if p := img.RGBAAt(400, 300); p.A != 255 {
		t.Errorf("photo pixel %v is not opaque", p)
	}
	t.Logf("backdrop pixel %v, photo pixel %v", bg, img.RGBAAt(400, 300))
}
