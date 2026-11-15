// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image/color"
	"sync"
	"sync/atomic"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"

	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// viewerWindow is the media viewer in a window of its own. It has its own
// viewer, decoders and textures: a window draws on its own GPU context and
// runs on its own goroutine, and shares only the store with the chat.
type viewerWindow struct {
	w       *appwindow.Window
	viewer  *mediaViewer
	images  imageOps
	catalog localization.Catalog
	theme   *token.Theme
	// themeFonts is the version of the fonts the theme was made with.
	themeFonts uint64
	closing    bool
	// prefs are the settings of the windows' blur and frame, which the viewer
	// follows as the main window does; effects is what it asked of w by them.
	prefs   *preferences.Store
	effects windowEffects
	// unsubscribe ends the window's watch on the settings, and asked counts
	// the redraws a change of them asked for.
	unsubscribe func()
	asked       atomic.Int64
}

func newViewerWindow(w *appwindow.Window, prefs *preferences.Store, source model.ConversationStore, catalog localization.Catalog, chat int64, current model.Message, known mediaList) *viewerWindow {
	p := &viewerWindow{w: w, catalog: catalog, prefs: prefs}
	// The window learns of a change of the settings made in another window
	// at once: it draws no frame of its own, but for an event or an
	// animation, and would take them at the next of these. Only the redraw
	// is asked from here, which any goroutine may do; the window's state is
	// changed by its own goroutine, in Update.
	p.unsubscribe = prefs.Subscribe(func() {
		p.asked.Add(1)
		w.Invalidate()
	})
	p.viewer = newMediaViewer(source, &p.images, w.Invalidate)
	p.viewer.standalone = true
	p.viewer.openList(chat, current, known)
	return p
}

// Theme is always dark: pictures are looked at on a dark background.
func (p *viewerWindow) Theme(gtx layout.Context) *token.Theme {
	if v := defaults.FontsVersion(); p.theme == nil || v != p.themeFonts {
		p.themeFonts = v
		p.theme = defaults.NewTheme(gtx, schemes.SchemeBaselineDark())
	}
	return p.theme
}

// Update asks the window for the blur and the frame the settings name, as
// the main window's are.
func (p *viewerWindow) Update(layout.Context) {
	p.effects.update(p.w, p.prefs.Global(), viewerWindowBlur)
}

func (p *viewerWindow) Layout(gtx layout.Context) {
	p.images.BeginFrame()
	defer p.images.EndFrame()
	transparent, blurred := p.w.Translucency()
	if p.w.OwnBlur() {
		// The window blurs by itself: the blurred wallpaper is under the
		// backdrop, which lets through what the compositor's blur would.
		blurred = p.w.LayoutDesktop(gtx, viewerBackdropBlurred, 1-float32(viewerBackdropBlurred.A)/255)
		transparent = blurred
	}
	switch {
	case blurred:
		p.viewer.backdropColor = viewerBackdropBlurred
	case transparent:
		p.viewer.backdropColor = viewerBackdropClear
	default:
		p.viewer.backdropColor = viewerBackdrop
	}
	p.viewer.Layout(gtx, p.catalog, p.w.Motion.AnimationsEnabled())
	// Escape and ✕ close the viewer, and with it the window.
	if !p.viewer.open && !p.closing {
		p.closing = true
		p.w.Perform(system.ActionClose)
	}
}

// RecoverFrame closes the viewer whose layout panicked, and with it the
// window, in the frame after.
func (p *viewerWindow) RecoverFrame() bool {
	return p.viewer.recoverFrame()
}

func (p *viewerWindow) Locale() system.Locale {
	return WindowLocale(p.catalog)
}

func (p *viewerWindow) SetSuspended(hidden bool) {
	if hidden {
		p.viewer.Release()
		p.images.Release()
	}
}

// Close is called by the window loop after the window is gone.
func (p *viewerWindow) Close() {
	p.unsubscribe()
	p.viewer.Destroy()
}

// viewerWindows are the viewer windows opened from one account window. They
// read that account's store, so they close with it.
type viewerWindows struct {
	mu   sync.Mutex
	open map[*appwindow.Window]struct{}
}

func (ws *viewerWindows) add(w *appwindow.Window) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.open == nil {
		ws.open = map[*appwindow.Window]struct{}{}
	}
	ws.open[w] = struct{}{}
}

func (ws *viewerWindows) remove(w *appwindow.Window) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.open, w)
}

func (ws *viewerWindows) closeAll() {
	ws.mu.Lock()
	windows := make([]*appwindow.Window, 0, len(ws.open))
	for w := range ws.open {
		windows = append(windows, w)
	}
	ws.mu.Unlock()
	for _, w := range windows {
		w.PerformLater(system.ActionClose)
	}
}

// openViewerWindow shows m of chat, among known, in a new window.
func (a *App) openViewerWindow(chat int64, m model.Message, known mediaList) {
	catalog := a.catalog()
	title := catalog.T("viewer.window")
	for _, c := range a.store.Chats() {
		if c.ID == chat {
			title += " — " + catalog.ChatTitle(c)
		}
	}
	source := a.history.source
	var opened *appwindow.Window
	// The desktop shows through as the viewer's blur setting says, blurred by
	// whom the windows' choice says, and the frame is the one they choose.
	opts := effectOptions(a.preferences.Global(), viewerWindowBlur)
	opts.Title, opts.Width, opts.Height, opts.Locale = title, unit.Dp(1100), unit.Dp(780), a.Locale()
	a.openWindow(appwindow.Spec{
		Options: opts,
		Build: func(w *appwindow.Window) appwindow.Content {
			opened = w
			a.viewerWindows.add(w)
			window := newViewerWindow(w, a.preferences, source, catalog, chat, m, known)
			a.configureViewer(window.viewer)
			return window
		},
		// Build and Closed run on the new window's goroutine, in that order.
		Closed: func() { a.viewerWindows.remove(opened) },
	})
}

// FrameFill implements appwindow.FrameFiller: the caption of the window's
// own frame is of the backdrop the picture lies on.
func (p *viewerWindow) FrameFill(gtx layout.Context) (fill, on color.NRGBA) {
	return p.viewer.backdropColor, scheme(gtx).Surface.OnColor.AsNRGBA()
}
