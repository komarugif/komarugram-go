// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// ownFrames makes the test one of a system where blurred windows draw their
// own frame, or of one where they do not.
func ownFrames(t *testing.T, own bool) {
	t.Helper()
	was, offered := ownFrame, ownFrameOffered
	ownFrame, ownFrameOffered = own, own
	t.Cleanup(func() { ownFrame, ownFrameOffered = was, offered })
}

// TestOwnFrameIsTheUsersChoice checks the frame a window is asked to draw
// itself: it has none of the system's, with the blur and without it, and
// has the system's again once it is not asked; where the system does not
// let a window frame itself, the choice changes nothing.
func TestOwnFrameIsTheUsersChoice(t *testing.T) {
	ownFrames(t, true)
	for _, blur := range []bool{false, true} {
		if cnf := configOf(effectOptions(blur, blur, true)); cnf.Decorated {
			t.Errorf("blur %v: a window asked to frame itself has the system's frame", blur)
		}
	}
	if cnf := configOf(effectOptions(false, false, false)); !cnf.Decorated {
		t.Error("a window not asked to frame itself, without blur, has no frame of the system's")
	}
	w := &Window{}
	w.SetEffects(true, true)
	if !w.OwnFrameForced() {
		t.Error("a window that blurs is not held to its own frame")
	}
	w.SetEffects(true, false)
	if w.OwnFrameForced() {
		t.Error("a window that does not blur is held to its own frame")
	}
	ownFrames(t, false)
	if cnf := configOf(effectOptions(false, false, true)); !cnf.Decorated {
		t.Error("a window asked to frame itself where it cannot has no frame of the system's")
	}
}

// configOf is the configuration the options make of a decorated window.
func configOf(opts []app.Option) app.Config {
	cnf := app.Config{Decorated: true}
	for _, opt := range opts {
		opt(unit.Metric{}, &cnf)
	}
	return cnf
}

func TestBlurTakesTheSystemFrameWhereWindowsOwnTheirs(t *testing.T) {
	ownFrames(t, true)
	for _, c := range []struct {
		transparent, blur, decorated bool
	}{
		{false, false, true},
		{true, false, true},
		// Blur is of a transparent window: alone it changes nothing.
		{false, true, true},
		{true, true, false},
	} {
		cnf := configOf(effectOptions(c.transparent, c.blur, false))
		if cnf.Transparent != c.transparent || cnf.BlurBehind != c.blur || cnf.Decorated != c.decorated {
			t.Errorf("transparent %v, blur %v: asked for transparent %v, blur %v, the system's frame %v", c.transparent, c.blur, cnf.Transparent, cnf.BlurBehind, cnf.Decorated)
		}
	}
	// Elsewhere the frame is not the window's to take off.
	ownFrames(t, false)
	if cnf := configOf(effectOptions(true, true, false)); !cnf.Decorated || !cnf.BlurBehind {
		t.Errorf("on a system that frames blurred windows: the system's frame %v, blur %v", cnf.Decorated, cnf.BlurBehind)
	}
}

func TestOwnFrameIsShownForAWindowWithoutTheSystems(t *testing.T) {
	ownFrames(t, true)
	var f frame
	gtx := layout.Context{Metric: unit.Metric{PxPerDp: 2, PxPerSp: 2}}
	for _, c := range []struct {
		name  string
		cnf   app.Config
		shown bool
	}{
		{"the system's frame", app.Config{Decorated: true}, false},
		{"no frame", app.Config{}, true},
		{"no frame, maximized", app.Config{Mode: app.Maximized}, true},
		{"no frame, fullscreen", app.Config{Mode: app.Fullscreen}, false},
	} {
		f.configure(c.cnf)
		want := 0
		if c.shown {
			want = 62
		}
		if f.shown != c.shown || f.height(gtx) != want {
			t.Errorf("%s: shown %v, %d pixels high, want %v and %d", c.name, f.shown, f.height(gtx), c.shown, want)
		}
		if f.maximized != (c.cnf.Mode == app.Maximized) {
			t.Errorf("%s: maximized %v", c.name, f.maximized)
		}
	}
	ownFrames(t, false)
	f.configure(app.Config{})
	if f.shown {
		t.Error("a frame of the window's own on a system that frames its windows")
	}
}

// frameContext is a frame context with the theme in it.
func frameContext(ops *op.Ops, size image.Point, dark bool) layout.Context {
	gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	scheme := schemes.SchemeBaselineLight()
	if dark {
		scheme = schemes.SchemeBaselineDark()
	}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, scheme))
	return gtx
}

func TestOwnFrameButtonsActOnTheWindow(t *testing.T) {
	ownFrames(t, true)
	var f frame
	f.configure(app.Config{})
	frame := func() system.Action {
		return f.layout(frameContext(new(op.Ops), image.Pt(400, 300), false), "Title", nil)
	}
	if got := frame(); got != 0 {
		t.Fatalf("actions without a click: %v", got)
	}
	for _, c := range []struct {
		maximized bool
		button    system.Action
		want      system.Action
	}{
		{false, system.ActionMinimize, system.ActionMinimize},
		{false, system.ActionMaximize, system.ActionMaximize},
		{true, system.ActionUnmaximize, system.ActionUnmaximize},
		{false, system.ActionClose, system.ActionClose},
	} {
		mode := app.Windowed
		if c.maximized {
			mode = app.Maximized
		}
		f.configure(app.Config{Mode: mode})
		frame()
		f.deco.Clickable(c.button).Click()
		if got := frame(); got != c.want {
			t.Errorf("button %v, maximized %v: the window is asked for %v, want %v", c.button, c.maximized, got, c.want)
		}
	}
	// A frame that is not shown takes no clicks.
	f.configure(app.Config{Decorated: true})
	f.deco.Clickable(system.ActionClose).Click()
	if got := frame(); got != 0 {
		t.Errorf("a frame that is not shown asked the window for %v", got)
	}
	// What the pointer did to a button is forgotten with the window hidden.
	f.configure(app.Config{})
	f.deco.Clickable(system.ActionMinimize).Click()
	f.configure(app.Config{Mode: app.Minimized})
	f.configure(app.Config{})
	if got := frame(); got != 0 {
		t.Errorf("a click from before the window was minimized asked for %v", got)
	}
}

func TestCaptionButtonColors(t *testing.T) {
	on := color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff}
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	if fill, icon := captionButtonColors(system.ActionClose, 0, false, on); fill.A != 0 || icon != on {
		t.Errorf("the close button at rest: fill %v, glyph %v", fill, icon)
	}
	if fill, icon := captionButtonColors(system.ActionClose, 1, false, on); fill != closeHover || icon != white {
		t.Errorf("the close button lit: fill %v, glyph %v", fill, icon)
	}
	// On the way it is the red, seen through, and a glyph between the two.
	fill, icon := captionButtonColors(system.ActionClose, 0.5, false, on)
	if fill.R != closeHover.R || fill.A < 0x70 || fill.A > 0x90 || icon.R <= on.R || icon.R >= white.R {
		t.Errorf("the close button half lit: fill %v, glyph %v", fill, icon)
	}
	rest, _ := captionButtonColors(system.ActionMinimize, 0, false, on)
	half, _ := captionButtonColors(system.ActionMinimize, 0.5, false, on)
	hover, _ := captionButtonColors(system.ActionMinimize, 1, false, on)
	press, icon := captionButtonColors(system.ActionMinimize, 1, true, on)
	if rest.A != 0 || half.A == 0 || half.A >= hover.A || press.A <= hover.A || icon != on || hover.R != on.R {
		t.Errorf("another button: at rest %v, half lit %v, lit %v, pressed %v, glyph %v", rest, half, hover, press, icon)
	}
}

func TestCaptionButtonsFadeInAndOut(t *testing.T) {
	var f frame
	now := time.Now()
	over := [3]bool{false, true, false}
	// The first frame under the pointer starts the fade, and asks for more.
	// However long ago the last frame was, the fade is at its start.
	f.litAt = now.Add(-time.Hour)
	if !f.light(now, over, true) || f.lit[1] != 0 {
		t.Fatalf("the first frame: lit %v", f.lit)
	}
	if !f.light(now.Add(captionFadeIn/2), over, true) || f.lit[1] < 0.4 || f.lit[1] > 0.6 {
		t.Fatalf("half the way in: lit %v", f.lit)
	}
	if f.light(now.Add(captionFadeIn*2), over, true) || f.lit[1] != 1 {
		t.Fatalf("after the way in: lit %v, and a frame asked for", f.lit)
	}
	if f.lit[0] != 0 || f.lit[2] != 0 {
		t.Errorf("the buttons the pointer is not over are lit: %v", f.lit)
	}
	// It fades once the pointer has left, slower than it lit up.
	now = f.litAt.Add(time.Hour)
	if !f.light(now, [3]bool{}, true) || f.lit[1] != 1 {
		t.Fatalf("the frame the pointer left in: lit %v", f.lit)
	}
	if !f.light(now.Add(captionFadeIn), [3]bool{}, true) || f.lit[1] <= 0.5 || f.lit[1] >= 1 {
		t.Fatalf("leaving: lit %v", f.lit)
	}
	if f.light(now.Add(captionFadeOut*2), [3]bool{}, true) || f.lit[1] != 0 {
		t.Fatalf("after the way out: lit %v, and a frame asked for", f.lit)
	}
	// Without animations there is no way: lit or not.
	if f.light(now, over, false) || f.lit[1] != 1 {
		t.Errorf("without animations: lit %v", f.lit)
	}
	if f.light(now, [3]bool{}, false) || f.lit[1] != 0 {
		t.Errorf("without animations, leaving: lit %v", f.lit)
	}
	// A window minimized by its button comes back with none lit.
	f.lit[0] = 1
	f.configure(app.Config{Mode: app.Minimized})
	if f.lit != [3]float32{} {
		t.Errorf("lit after the window was minimized: %v", f.lit)
	}
}

// filled is content that names the colors of the frame.
type filled struct{ fill, on color.NRGBA }

func (filled) Theme(layout.Context) *token.Theme { return nil }
func (filled) Update(layout.Context)             {}
func (filled) Layout(layout.Context)             {}
func (c filled) FrameFill(layout.Context) (fill, on color.NRGBA) {
	return c.fill, c.on
}

// TestOwnFrameIsDrawn draws the frame, and checks where: the caption is of
// the content's color, the window below it is left to the content, and the
// close button under the pointer is red. FRAME_PNG_DIR saves the frames of
// both themes to look at.
func TestOwnFrameIsDrawn(t *testing.T) {
	ownFrames(t, true)
	size := image.Pt(480, 120)
	window, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skipf("no headless window: %v", err)
	}
	defer window.Release()
	draw := func(f *frame, dark bool, content Content) *image.RGBA {
		ops := new(op.Ops)
		gtx := frameContext(ops, size, dark)
		f.layout(gtx, "KomaruGram Go — Ada Lovelace", content)
		if err := window.Frame(ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: size})
		if err := window.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		return img
	}
	var f frame
	f.configure(app.Config{})
	fill := color.NRGBA{R: 0x20, G: 0x40, B: 0x60, A: 0xff}
	img := draw(&f, false, filled{fill: fill, on: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}})
	if got := color.NRGBAModel.Convert(img.At(200, 4)); got != fill {
		t.Errorf("the caption is %v, want the content's %v", got, fill)
	}
	if _, _, _, a := img.At(200, 60).RGBA(); a != 0 {
		t.Errorf("the frame painted below the caption: %v", img.At(200, 60))
	}
	// The glyphs are there: something of the glyph's color in each button.
	for i := range 3 {
		found := false
		for x := size.X - (3-i)*46; x < size.X-(2-i)*46; x++ {
			for y := range 31 {
				r, _, _, _ := img.At(x, y).RGBA()
				found = found || r > 0xc000
			}
		}
		if !found {
			t.Errorf("button %d has no glyph", i)
		}
	}
	if dir := os.Getenv("FRAME_PNG_DIR"); dir != "" {
		for name, dark := range map[string]bool{"light": false, "dark": true} {
			var f frame
			f.configure(app.Config{Mode: map[bool]app.WindowMode{false: app.Windowed, true: app.Maximized}[dark]})
			out, err := os.Create(filepath.Join(dir, "frame-"+name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(out, draw(&f, dark, nil)); err != nil {
				t.Fatal(err)
			}
			out.Close()
		}
	}
}

// TestOwnFrameHasABorder checks the line around the window: a pixel of the
// theme's outline on every side, opaque, which covers the line the system
// draws behind the top of the window, with the content inside left as it
// is; and none around a maximized window or one with the system's frame.
func TestOwnFrameHasABorder(t *testing.T) {
	ownFrames(t, true)
	size := image.Pt(200, 120)
	window, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skipf("no headless window: %v", err)
	}
	defer window.Release()
	draw := func(cnf app.Config) (*image.RGBA, color.NRGBA) {
		var f frame
		f.configure(cnf)
		ops := new(op.Ops)
		gtx := frameContext(ops, size, false)
		f.layoutBorder(gtx, size)
		if err := window.Frame(ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: size})
		if err := window.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		line := wdk.GetMaterialTheme(gtx).Scheme.OutlineVariant.AsNRGBA()
		line.A = 0xff
		return img, line
	}
	img, line := draw(app.Config{})
	for name, at := range map[string]image.Point{
		"the top": {X: 100, Y: 0}, "the bottom": {X: 100, Y: 119}, "the left": {X: 0, Y: 60}, "the right": {X: 199, Y: 60},
		"a corner": {X: 0, Y: 0}, "the opposite corner": {X: 199, Y: 119},
	} {
		if got := color.NRGBAModel.Convert(img.At(at.X, at.Y)); got != line {
			t.Errorf("%s of the window is %v, want the border's %v", name, got, line)
		}
	}
	for name, at := range map[string]image.Point{
		"below the top": {X: 100, Y: 1}, "above the bottom": {X: 100, Y: 118}, "beside the left": {X: 1, Y: 60}, "beside the right": {X: 198, Y: 60}, "the middle": {X: 100, Y: 60},
	} {
		if _, _, _, a := img.At(at.X, at.Y).RGBA(); a != 0 {
			t.Errorf("the border is painted %s: %v", name, img.At(at.X, at.Y))
		}
	}
	for name, cnf := range map[string]app.Config{
		"a maximized window":         {Mode: app.Maximized},
		"a window the system frames": {Decorated: true},
		"a fullscreen window":        {Mode: app.Fullscreen},
	} {
		img, _ := draw(cnf)
		if _, _, _, a := img.At(100, 0).RGBA(); a != 0 {
			t.Errorf("%s has a border: %v", name, img.At(100, 0))
		}
	}
}
