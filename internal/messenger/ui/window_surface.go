// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/preferences"

	"gio-mw/token"
	"gioui.org/layout"
)

const windowSurfaceOpacityKey = "komarugram/ui.windowSurfaceOpacity"

// The compositor blurs the desktop. Only the surface fill is translucent;
// applying an opacity layer to a widget would also fade its text and media.
func withWindowSurfaceOpacity(gtx layout.Context, transparency int, supported bool) {
	opacity := token.OpacityLevel(1)
	if supported {
		opacity = token.OpacityLevel(1 - float32(transparency)/100)
	}
	if gtx.Values != nil {
		gtx.Values[windowSurfaceOpacityKey] = opacity
	}
}

func fillWindowSurface(gtx layout.Context, fill token.MatColor, size image.Point) {
	if opacity, ok := gtx.Values[windowSurfaceOpacityKey].(token.OpacityLevel); ok {
		fill = fill.SetOpacity(opacity)
	}
	fillRect(gtx, fill, size)
}

// Keep transparency available for live changes to the slider, but request
// compositor blur only when enabled and some desktop can show through. Other backends
// ignore these options and report opaque windows through Translucency.
//
// A window that blurs by itself is opaque to the system, and draws the
// blurred wallpaper under its surfaces (layoutWindow): where the compositor
// has no blur, or where the user chose so.
func (a *App) updateWindowEffects() {
	a.windowFx.update(a.window, a.preferences.Global(), mainWindowBlur)
}

// blurAsked tells, from the settings, whether a window asks for blur behind
// it, and whether its surfaces are translucent at all.
type blurAsked func(preferences.Global) (blur, translucent bool)

// mainWindowBlur is the main window's: its blur is a checkbox of its own, and
// it takes the transparency of its surfaces.
func mainWindowBlur(prefs preferences.Global) (blur, translucent bool) {
	translucent = prefs.WindowTransparency > 0
	return prefs.WindowBlur && translucent, translucent
}

// viewerWindowBlur is the media viewer's: its blur is a checkbox of its own,
// and its backdrop has the alpha of its own.
func viewerWindowBlur(prefs preferences.Global) (blur, translucent bool) {
	return prefs.ViewerBlur, prefs.ViewerBlur
}

// windowEffects is what a window was last asked for by the settings of the
// windows' blur and frame: the main window's and the viewer's alike.
type windowEffects struct {
	set, blur, transparent bool
}

// update asks w for the effects prefs name, when they changed, and reports
// whether w blurs the desktop by itself. Who blurs, the compositor or the
// window, is the one choice of all the windows; asked is the window's own.
func (e *windowEffects) update(w *appwindow.Window, prefs preferences.Global, asked blurAsked) (own bool) {
	blur, translucent := asked(prefs)
	own = blur && w.OwnBlurSupported() && (prefs.WindowBlurOwn || !w.CompositorBlurs())
	want := blur && !own
	transparent := !own && appwindow.WantsTransparent(translucent)
	w.SetOwnFrame(prefs.WindowOwnFrame)
	if e.set && e.blur == want && e.transparent == transparent && w.OwnBlur() == own {
		return own
	}
	e.set, e.blur, e.transparent = true, want, transparent
	w.SetOwnBlur(own)
	w.SetEffects(transparent, want)
	return own
}

// WindowEffectOptions are the options of the main window that follow the
// settings of the windows' blur and frame, for it to start with them.
func WindowEffectOptions(prefs preferences.Global) appwindow.Options {
	return effectOptions(prefs, mainWindowBlur)
}

// effectOptions are the options of a window that follow the settings, its
// blur as asked tells. A window that blurs by itself is opaque to the
// system. Where that is known before there is a window, it starts so, not to
// change its frame at its first frame.
func effectOptions(prefs preferences.Global, asked blurAsked) appwindow.Options {
	blur, translucent := asked(prefs)
	own := prefs.WindowBlurOwn && appwindow.OwnBlurFromStart()
	return appwindow.Options{
		Transparent: !own && appwindow.WantsTransparent(translucent),
		BlurBehind:  !own && blur,
		OwnFrame:    prefs.WindowOwnFrame,
	}
}

// FrameFill implements appwindow.FrameFiller: the caption of the window's
// own frame is a surface of the window, as the sidebar below it is.
func (a *App) FrameFill(gtx layout.Context) (fill, on color.NRGBA) {
	sc := scheme(gtx)
	surface := sc.SurfaceContainer
	if transparent, _ := a.window.Translucency(); transparent || a.window.DesktopDrawn() {
		surface = surface.SetOpacity(token.OpacityLevel(1 - float32(a.preferences.Global().WindowTransparency)/100))
	}
	return surface.AsNRGBA(), sc.Surface.OnColor.AsNRGBA()
}
