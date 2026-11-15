// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"strconv"

	"gio-mw/token"
	"gio-mw/widget/slider"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/wallpaper"
)

// Windows 7 without Aero, under the Basic and Classic themes, offers no
// color for the windows: its Window Color is the classic dialog there, and
// the glass keeps the color the theme wrote, Sky. The appearance offers the
// personalization's colors and its intensity then (preferences.WindowColor),
// for the glass the window draws by itself and the system's accent.

// windowColorNames are the keys of the names of wallpaper.WindowColors.
var windowColorNames = [len(wallpaper.WindowColors)]string{
	"settings.window_color.sky", "settings.window_color.twilight", "settings.window_color.sea", "settings.window_color.leaf",
	"settings.window_color.lime", "settings.window_color.sun", "settings.window_color.pumpkin", "settings.window_color.ruby",
	"settings.window_color.fuchsia", "settings.window_color.blush", "settings.window_color.violet", "settings.window_color.lavender",
	"settings.window_color.taupe", "settings.window_color.chocolate", "settings.window_color.slate", "settings.window_color.frost",
}

// The intensity slider's stops, in percent of the alpha: Windows 7's
// slider goes from 0x1a to 0xd9 of it, 10 to 85 %.
const (
	intensityStep = 5
	intensityLow  = 10
	intensityHigh = 85
)

// windowColorSettings is the card of the color of the windows.
type windowColorSettings struct {
	// color is the color chosen, 0xAARRGGBB, 0 for the system's; system is
	// the system's; offered reports whether the card is shown.
	color    func() uint32
	setColor func(uint32)
	system   func() uint32
	offered  func() bool

	swatches  [len(wallpaper.WindowColors)]surface
	reset     surface
	intensity *slider.Slider
}

func newWindowColorSettings() *windowColorSettings {
	w := &windowColorSettings{}
	var stops []int
	for v := intensityLow; v <= intensityHigh; v += intensityStep {
		stops = append(stops, v)
	}
	w.intensity = slider.StandardSlider(stops, intensityLow, func(percent int) {
		alpha := uint32(min(max((percent*255+50)/100, wallpaper.IntensityMin), wallpaper.IntensityMax))
		w.choose(w.current()&0xffffff | alpha<<24)
	})
	return w
}

// shown reports whether the card is shown.
func (w *windowColorSettings) shown() bool {
	return w.offered != nil && w.offered() && w.color != nil
}

// current is the color the windows have: the one chosen, or the system's.
func (w *windowColorSettings) current() uint32 {
	if w.color != nil {
		if c := w.color(); c != 0 {
			return c
		}
	}
	if w.system != nil {
		if c := w.system(); c != 0 {
			return c
		}
	}
	return wallpaper.WindowColors[0]
}

func (w *windowColorSettings) choose(argb uint32) {
	if w.setColor != nil {
		w.setColor(argb)
	}
}

// intensityPercent is the slider's stop nearest the color's intensity.
func intensityPercent(argb uint32) int {
	percent := int(argb>>24) * 100 / 255
	percent = (percent + intensityStep/2) / intensityStep * intensityStep
	return min(max(percent, intensityLow), intensityHigh)
}

// Update takes the clicks of the card, and follows the color chosen
// elsewhere: another window, the settings' file.
func (w *windowColorSettings) Update(gtx layout.Context) {
	if !w.shown() {
		return
	}
	for i := range w.swatches {
		if w.swatches[i].Clicked(gtx) {
			// A color at the intensity it starts with, as Windows does.
			w.choose(wallpaper.WindowColors[i])
		}
	}
	if w.reset.Clicked(gtx) {
		w.choose(0)
	}
	if v := intensityPercent(w.current()); w.intensity.GetValue() != v {
		w.intensity.SetValue(v)
	}
}

// name is the name of the color, its swatch's, or the custom one's.
func (w *windowColorSettings) name(argb uint32, l localization.Catalog) string {
	for i, c := range wallpaper.WindowColors {
		if c&0xffffff == argb&0xffffff {
			return l.T(windowColorNames[i])
		}
	}
	return l.T("settings.window_color_custom")
}

// Layout draws the card: the colors as circles, the one the windows have
// ringed, its name, and the slider of its intensity.
func (w *windowColorSettings) Layout(gtx layout.Context, height *heightTransition, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	current := w.current()
	chosen := w.color != nil && w.color() != 0
	name := w.name(current, l)
	if !chosen {
		name = l.Format("settings.window_color_as_system", map[string]string{"name": name})
	}
	return height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("settings.window_color"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
			}),
			vspace(12),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return w.layoutSwatches(gtx, current, l)
			}),
			vspace(12),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.Format("settings.window_color_current", map[string]string{"name": name}), token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
			}),
		}
		if chosen {
			children = append(children, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return textButton(gtx, &w.reset, l.T("settings.window_color_system"))
			}))
		}
		children = append(children, vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("settings.window_color_intensity")+": "+strconv.Itoa(int(current>>24)*100/255)+"%", token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
			}),
			layout.Rigid(w.intensity.Layout),
			vspace(4),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("settings.window_color_hint"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}, defaultCardPadding)
}

// layoutSwatches draws the colors in rows of eight, as Windows does, or of
// as many as fit.
func (w *windowColorSettings) layoutSwatches(gtx layout.Context, current uint32, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	d, gap := gtx.Dp(accentSize), gtx.Dp(12)
	cols := min(8, max(1, (gtx.Constraints.Max.X+gap)/(d+gap)))
	for i, c := range wallpaper.WindowColors {
		rgb := color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xff}
		at := image.Pt(i%cols*(d+gap), i/cols*(d+gap))
		offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(d, d)
			inset := 0
			if c&0xffffff == current&0xffffff {
				ring := clip.Ellipse{Max: size}.Path(gtx.Ops)
				paint.FillShape(gtx.Ops, sc.Surface.OnColor.AsNRGBA(), clip.Stroke{Path: ring, Width: float32(gtx.Dp(2))}.Op())
				inset = gtx.Dp(5)
			}
			circle := clip.Ellipse{Min: image.Pt(inset, inset), Max: size.Sub(image.Pt(inset, inset))}
			paint.FillShape(gtx.Ops, rgb, circle.Op(gtx.Ops))
			// Frost and the light colors keep an edge on a light surface.
			paint.FillShape(gtx.Ops, sc.OutlineVariant.AsNRGBA(), clip.Stroke{Path: circle.Path(gtx.Ops), Width: float32(gtx.Dp(1))}.Op())
			return w.swatches[i].Layout(gtx, size, surfaceStyle{radius: d / 2, content: sc.Surface.OnColor, button: l.T(windowColorNames[i])}, nil)
		})
	}
	rows := (len(wallpaper.WindowColors) + cols - 1) / cols
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rows*(d+gap)-gap)}
}
