// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"

	"komarugram/internal/appwindow"

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
	prefs := a.preferences.Global()
	blur := prefs.WindowBlur && prefs.WindowTransparency > 0
	own := blur && a.window.OwnBlurSupported() && (prefs.WindowBlurOwn || !a.window.CompositorBlurs())
	want := blur && !own
	transparent := !own && appwindow.WantsTransparent(prefs.WindowTransparency > 0)
	a.window.SetOwnFrame(prefs.WindowOwnFrame)
	if a.windowEffectsSet && a.windowBlurWanted == want && a.windowTransparentWanted == transparent && a.window.OwnBlur() == own {
		return
	}
	a.windowEffectsSet, a.windowBlurWanted, a.windowTransparentWanted = true, want, transparent
	a.window.SetOwnBlur(own)
	a.window.SetEffects(transparent, want)
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
