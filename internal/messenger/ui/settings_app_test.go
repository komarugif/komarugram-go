// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp"
	"gio-mw/exp/appearance"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// settingsApp is the settings of a window as the client makes them: the
// window's App, New on the demo's store, its settings in a file of the
// test's own. What the settings show is set where the client keeps it, in
// the preferences, before newSettingsApp; only what comes from the system
// (what players are found, what the desktop takes) is set on the page.
type settingsApp struct {
	t     *testing.T
	app   *App
	prefs *preferences.Store
	store *mockstore.Store
	size  image.Point
	ops   op.Ops
}

// newSettingsPrefs is a file of preferences of the test's own, beside
// which the client keeps its emoji packs, in Russian.
func newSettingsPrefs(t *testing.T) *preferences.Store {
	t.Helper()
	prefs, err := preferences.OpenPath(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := prefs.SetLanguage("ru"); err != nil {
		t.Fatal(err)
	}
	return prefs
}

// newSettingsApp opens the settings of a window built with services, in
// a window that can be transparent when transparent is set, and draws
// their main page once, as the window shows it first.
func newSettingsApp(t *testing.T, prefs *preferences.Store, services Services, transparent bool, size image.Point) *settingsApp {
	t.Helper()
	// A system that tells its scheme, light, as most do.
	w := &appwindow.Window{Motion: motion.New(func() {}), Appearance: appearance.Fixed(appearance.Light)}
	t.Cleanup(w.Motion.Close)
	w.SetTranslucency(transparent, false)
	services.Preferences = prefs
	if services.MiniApps == nil {
		services.MiniApps = miniappprefs.New(miniapp.Ephemeral)
	}
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, services)
	t.Cleanup(a.Close)
	h := &settingsApp{t: t, app: a, prefs: prefs, store: store, size: size}
	h.frame(func(gtx layout.Context) { a.openSection(gtx, section{kind: sectionSettings}) })
	h.frame(nil)
	return h
}

// context is a frame's context, the theme as the window's.
func (h *settingsApp) context() layout.Context {
	h.ops.Reset()
	gtx := layout.Context{Ops: &h.ops, Now: time.Now(), Constraints: layout.Exact(h.size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
	sc := schemes.SchemeBaselineLight()
	if h.app.dark() {
		sc = schemes.SchemeBaselineDark()
	}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, sc))
	return gtx
}

// draw draws the settings as the window does, with the dialogs over them.
func (h *settingsApp) draw(gtx layout.Context) {
	a := h.app
	l := localization.For(h.prefs.Global().Language)
	a.images.BeginFrame()
	// The root surface, which sections drawn by exp widgets read.
	exp.Background(gtx)
	a.settings.Update(gtx, a.themeMode(), h.prefs.Global().Language)
	a.settings.Layout(gtx, a.themeMode(), a.window.Appearance.Scheme(), a.dark(), l)
	a.settings.sessions.layoutDialog(gtx, l)
	a.settings.chats.layoutDialog(gtx, l)
	a.images.EndFrame()
}

// frame draws a frame, after before when it is set.
func (h *settingsApp) frame(before func(gtx layout.Context)) {
	gtx := h.context()
	if before != nil {
		before(gtx)
	}
	h.draw(gtx)
}

// show opens a section, as a click on its row does, and draws it.
func (h *settingsApp) show(s settingsSection) {
	h.app.settings.show(s)
	h.frame(nil)
}

// until draws frames until done, what runs in the background having come
// back, and fails after 20 seconds.
func (h *settingsApp) until(what string, done func() bool) {
	h.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); !done(); {
		if time.Now().After(deadline) {
			h.t.Fatalf("%s never came", what)
		}
		time.Sleep(20 * time.Millisecond)
		h.frame(nil)
	}
}

// settle draws frames for d, for what moves to come to rest: the height
// of a card, what the settings read again once a second.
func (h *settingsApp) settle(d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
		h.frame(nil)
	}
}

// ready waits for what the settings look for in the background to come
// back, for a picture of them: the players and the browser of Mini Apps;
// and for the cards to take their heights.
func (h *settingsApp) ready() {
	h.t.Helper()
	h.until("the players found", func() bool { return h.app.settings.players.checked })
	h.settle(400 * time.Millisecond)
}

// render draws the settings on a GPU and saves them to path, a PNG.
func (h *settingsApp) render(path string) {
	h.t.Helper()
	h.ready()
	win, err := headless.NewWindow(h.size.X, h.size.Y)
	if err != nil {
		h.t.Fatal(err)
	}
	defer win.Release()
	gtx := h.context()
	h.draw(gtx)
	if err := win.Frame(gtx.Ops); err != nil {
		h.t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: h.size})
	if err := win.Screenshot(img); err != nil {
		h.t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		h.t.Fatal(err)
	}
}
