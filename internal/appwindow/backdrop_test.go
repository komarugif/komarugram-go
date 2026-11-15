// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/event"
	"gioui.org/op"

	"komarugram/internal/wallpaper"
)

// fakeDesktop is a desktop with the window at a place on it.
type fakeDesktop struct {
	desktop *wallpaper.Desktop
	origin  image.Point
}

func (f *fakeDesktop) Ready() bool                 { return true }
func (f *fakeDesktop) Desktop() *wallpaper.Desktop { return f.desktop }
func (f *fakeDesktop) Origin() (image.Point, bool) { return f.origin, true }
func (f *fakeDesktop) Event(event.Event)           {}
func (f *fakeDesktop) Close()                      {}

// TestDesktopStaysInPlaceUnderAMovingWindow checks the window's own blur:
// the window draws the part of the desktop it is over, so that a point of
// the desktop is drawn at another place in a window that has moved, and
// stays where it is on the screen. Two screens side by side, red and blue,
// are each drawn where they are. The window draws it under all of itself,
// before its frame and its content, once the content has asked for it.
func TestDesktopStaysInPlaceUnderAMovingWindow(t *testing.T) {
	size := image.Pt(200, 100)
	window, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skipf("no headless window: %v", err)
	}
	defer window.Release()
	picture := func(c color.Color) *image.RGBA {
		im := image.NewRGBA(image.Rect(0, 0, 32, 18))
		draw.Draw(im, im.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
		return im
	}
	// The colors as they are drawn under surfaces of a color.
	fill, show := color.NRGBA{R: 0xf0, G: 0xf0, B: 0xf0, A: 0xff}, float32(0.5)
	drawn := func(c color.NRGBA) color.NRGBA {
		return color.NRGBAModel.Convert(wallpaper.Acrylic(picture(c), fill, float64(show)).At(0, 0)).(color.NRGBA)
	}
	red, blue := color.NRGBA{R: 0xff, A: 0xff}, color.NRGBA{B: 0xff, A: 0xff}
	source := &fakeDesktop{desktop: &wallpaper.Desktop{Screens: []wallpaper.Screen{
		{Bounds: image.Rect(0, 0, 1920, 1080), Picture: picture(red)},
		{Bounds: image.Rect(1920, 0, 3840, 1080), Picture: picture(blue)},
	}}}
	w := &Window{}
	w.backdrop.source, w.backdrop.tried, w.backdrop.on = source, true, true
	// The content asks for the desktop in a frame, and has it from the next.
	if w.LayoutDesktop(frameContext(new(op.Ops), size, false), fill, show) {
		t.Error("the desktop is drawn in the frame it was first asked for in")
	}
	shot := func(origin image.Point) *image.RGBA {
		source.origin = origin
		ops := new(op.Ops)
		gtx := frameContext(ops, size, false)
		w.layoutBackdrop(gtx, size)
		if !w.LayoutDesktop(gtx, fill, show) {
			t.Fatalf("at %v: no desktop drawn", origin)
		}
		if err := window.Frame(ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: size})
		if err := window.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		return img
	}
	// The border of the screens is at 1920 on the desktop.
	for _, origin := range []image.Point{{X: 1820, Y: 300}, {X: 1870, Y: 40}} {
		img := shot(origin)
		border := 1920 - origin.X
		if got := color.NRGBAModel.Convert(img.At(border-2, 50)); got != drawn(red) {
			t.Errorf("window at %v: left of the screens' border is %v, want red", origin, got)
		}
		if got := color.NRGBAModel.Convert(img.At(border+1, 50)); got != drawn(blue) {
			t.Errorf("window at %v: right of the screens' border is %v, want blue", origin, got)
		}
	}
	// Over no screen there is nothing to draw.
	source.origin = image.Pt(-5000, 0)
	w.layoutBackdrop(frameContext(new(op.Ops), size, false), size)
	if w.DesktopDrawn() {
		t.Error("a window over no screen drew the desktop")
	}
}

// TestGlassStreaksFollowTheWindow checks the glass of Windows 7 a window
// draws by itself: its streaks move with the window, by an eighth of how
// far it went, once the picture is made anew in the background; and the
// window draws the picture it had meanwhile.
func TestGlassStreaksFollowTheWindow(t *testing.T) {
	size := image.Pt(400, 300)
	picture := image.NewRGBA(image.Rect(0, 0, 960, 540))
	streaks := image.NewRGBA(picture.Bounds())
	glass := wallpaper.SkyGlass
	screen := wallpaper.Screen{Bounds: image.Rect(0, 0, 1920, 1080), Picture: picture, Reflection: streaks}
	source := &fakeDesktop{desktop: &wallpaper.Desktop{Screens: []wallpaper.Screen{screen}, Glass: &glass}}
	w := &Window{}
	w.backdrop.source, w.backdrop.tried, w.backdrop.on = source, true, true
	fill := color.NRGBA{R: 0xf0, G: 0xf0, B: 0xf0, A: 0xff}
	frame := func() {
		gtx := frameContext(new(op.Ops), size, false)
		w.layoutBackdrop(gtx, size)
		w.LayoutDesktop(gtx, fill, 0.5)
	}
	// The window's center at the screen's: the streaks as stretched.
	source.origin = image.Pt(960-200, 300)
	frame()
	frame()
	if !w.DesktopDrawn() || w.backdrop.images[pictureKey{picture: picture, glass: w.backdrop.glass}].shift != 0 {
		t.Fatalf("at the center: drawn %t, streaks moved by %d", w.DesktopDrawn(), w.backdrop.images[pictureKey{picture: picture, glass: w.backdrop.glass}].shift)
	}
	// 320 pixels to the right are 160 of the picture; an eighth is 20.
	source.origin.X += 320
	deadline := time.Now().Add(5 * time.Second)
	for frame(); w.backdrop.images[pictureKey{picture: picture, glass: w.backdrop.glass}].shift != 20; frame() {
		if !w.DesktopDrawn() {
			t.Fatal("the window drew no desktop while its glass was made anew")
		}
		if time.Now().After(deadline) {
			t.Fatalf("the streaks are moved by %d, want 20", w.backdrop.images[pictureKey{picture: picture, glass: w.backdrop.glass}].shift)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
