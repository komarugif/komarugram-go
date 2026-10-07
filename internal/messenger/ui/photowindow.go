// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image/color"
	"sync"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"

	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// photoWindow is the photo viewer in a window of its own. It has its own
// viewer, decoders and textures: a window draws on its own GPU context and
// runs on its own goroutine, and shares only the store with the chat.
type photoWindow struct {
	w       *appwindow.Window
	viewer  *photoViewer
	images  imageOps
	catalog localization.Catalog
	theme   *token.Theme
	// themeFonts is the version of the fonts the theme was made with.
	themeFonts uint64
	closing    bool
}

func newPhotoWindow(w *appwindow.Window, source model.ConversationStore, catalog localization.Catalog, chat int64, current model.Message, known photoList) *photoWindow {
	p := &photoWindow{w: w, catalog: catalog}
	p.viewer = newPhotoViewer(source, &p.images, w.Invalidate)
	p.viewer.standalone = true
	p.viewer.openList(chat, current, known)
	return p
}

// Theme is always dark: photos are looked at on a dark background.
func (p *photoWindow) Theme(gtx layout.Context) *token.Theme {
	if v := defaults.FontsVersion(); p.theme == nil || v != p.themeFonts {
		p.themeFonts = v
		p.theme = defaults.NewTheme(gtx, schemes.SchemeBaselineDark())
	}
	return p.theme
}

func (p *photoWindow) Update(layout.Context) {}

func (p *photoWindow) Layout(gtx layout.Context) {
	p.images.BeginFrame()
	defer p.images.EndFrame()
	switch transparent, blurred := p.w.Translucency(); {
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

func (p *photoWindow) Locale() system.Locale {
	return system.Locale{Language: string(p.catalog.Language()), Direction: system.LTR}
}

func (p *photoWindow) SetSuspended(hidden bool) {
	if hidden {
		p.viewer.Release()
		p.images.Release()
	}
}

// Close is called by the window loop after the window is gone.
func (p *photoWindow) Close() { p.viewer.Destroy() }

// photoWindows are the viewer windows opened from one account window. They
// read that account's store, so they close with it.
type photoWindows struct {
	mu   sync.Mutex
	open map[*appwindow.Window]struct{}
}

func (ws *photoWindows) add(w *appwindow.Window) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.open == nil {
		ws.open = map[*appwindow.Window]struct{}{}
	}
	ws.open[w] = struct{}{}
}

func (ws *photoWindows) remove(w *appwindow.Window) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.open, w)
}

func (ws *photoWindows) closeAll() {
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

// openPhotoWindow shows photo m of chat in a new window.
func (a *App) openPhotoWindow(chat int64, m model.Message, known photoList) {
	catalog := a.catalog()
	title := catalog.T("viewer.window")
	for _, c := range a.store.Chats() {
		if c.ID == chat {
			title += " — " + c.Title
		}
	}
	source := a.history.source
	var opened *appwindow.Window
	a.openWindow(appwindow.Spec{
		Options: appwindow.Options{
			Title: title, Width: unit.Dp(1100), Height: unit.Dp(780), Locale: a.Locale(),
			// The desktop shows through, blurred where the compositor can.
			Transparent: true, BlurBehind: true,
		},
		Build: func(w *appwindow.Window) appwindow.Content {
			opened = w
			a.photoWindows.add(w)
			return newPhotoWindow(w, source, catalog, chat, m, known)
		},
		// Build and Closed run on the new window's goroutine, in that order.
		Closed: func() { a.photoWindows.remove(opened) },
	})
}

// FrameFill implements appwindow.FrameFiller: the caption of the window's
// own frame is of the backdrop the photo lies on.
func (p *photoWindow) FrameFill(gtx layout.Context) (fill, on color.NRGBA) {
	return p.viewer.backdropColor, scheme(gtx).Surface.OnColor.AsNRGBA()
}
