// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"slices"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/io/input"
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

// TestSettingsOpenEverySection opens every section of the settings the way
// a user does, by its row on the main page, in the whole window as it draws
// itself, and draws it for a few frames: in both languages, both themes, and
// a narrow and a wide window. It draws the log-out confirmation and the
// gallery of wallpapers too.
//
// The sections are laid out by the window, not on their own as the render
// tests draw them: those set up what the window sets up around them, and so
// missed sections that panicked in the window only, as the Mini Apps and
// animation settings did when the window's root surface went missing.
//
// A section is found by its title, which every section has: a new one is
// tested without being named here. Each opens in a window of its own, so
// that one that panics does not hide the others: a frame that panicked
// leaves the window's ops unfinished.
func TestSettingsOpenEverySection(t *testing.T) {
	var sections []settingsSection
	for s := range settingsTitles(localization.For("ru")) {
		sections = append(sections, s)
	}
	slices.Sort(sections)
	titles := settingsTitles(localization.For("en"))
	for _, language := range []string{"ru", "en"} {
		for theme, themeName := range map[preferences.Theme]string{preferences.ThemeLight: "light", preferences.ThemeDark: "dark"} {
			for _, size := range []image.Point{{360, 640}, {1280, 800}} {
				t.Run(fmt.Sprintf("%s/%s/%dx%d", language, themeName, size.X, size.Y), func(t *testing.T) {
					for _, s := range sections {
						w := newSettingsWindow(t, language, theme, size)
						w.open(s, titles[s])
						w.close()
					}
				})
			}
		}
	}
}

// settingsWindow is a window of the application drawn by hand, on the
// settings.
type settingsWindow struct {
	t      *testing.T
	a      *App
	window *appwindow.Window
	router input.Router
	size   image.Point
}

func newSettingsWindow(t *testing.T, language string, theme preferences.Theme, size image.Point) *settingsWindow {
	prefs := preferences.Memory()
	if err := prefs.SetLanguage(language); err != nil {
		t.Fatal(err)
	}
	if err := prefs.SetTheme(theme); err != nil {
		t.Fatal(err)
	}
	window := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	a := New(window, mockstore.New(time.Now(), 0), Services{Preferences: prefs, MiniApps: miniappprefs.New(miniapp.Ephemeral)})
	t.Cleanup(a.Close)
	a.section = section{kind: sectionSettings}
	a.settings.open()
	return &settingsWindow{t: t, a: a, window: window, size: size}
}

func (w *settingsWindow) close() {
	w.a.Close()
	w.window.Motion.Close()
}

// frames draws the window n times, and reports whether it drew them all:
// a panic is an error of the test, what names what was drawn.
func (w *settingsWindow) frames(what string, n int) bool {
	for range n {
		ok := func() (ok bool) {
			defer func() {
				if failure := recover(); failure != nil {
					w.t.Errorf("%s: %v", what, failure)
				}
			}()
			gtx := layout.Context{Ops: new(op.Ops), Source: w.router.Source(), Now: time.Now(), Constraints: layout.Exact(w.size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
			w.a.Update(gtx)
			w.a.Layout(gtx)
			w.router.Frame(gtx.Ops)
			return true
		}()
		if !ok {
			return false
		}
	}
	return true
}

// open opens section s from the main page and draws it, with what it
// shows on top: the log-out confirmation of the main page, the gallery of
// wallpapers of the chats' settings.
func (w *settingsWindow) open(s settingsSection, title string) {
	a := w.a
	if !w.frames("main page", 2) {
		return
	}
	if s == settingsMain {
		a.settings.confirmingLogOut = true
		w.frames("log-out confirmation", 2)
		return
	}
	item := a.settings.items[s]
	if item == nil {
		w.t.Errorf("%s: no row on the main page", title)
		return
	}
	item.click.Click()
	if !w.frames(title, 4) {
		return
	}
	if a.settings.section != s {
		w.t.Errorf("%s: its row opened section %d", title, a.settings.section)
		return
	}
	if s == settingsChats && a.settings.chats.available() {
		a.settings.chats.gallery.open()
		if !w.frames(title+": wallpapers", 3) {
			return
		}
		a.settings.chats.gallery.dialog.Close()
		w.frames(title+": wallpapers closed", 2)
	}
}
