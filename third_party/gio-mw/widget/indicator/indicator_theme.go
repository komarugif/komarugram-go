// SPDX-License-Identifier: Unlicense OR MIT

package indicator

import (
	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/unit"
)

const Namespace = "gio-mw.widget.indicator.Indicator"

type Theme struct {
	EnabledActiveIndicatorColor     token.MatColor
	EnabledActiveIndicatorShape     token.CornerShapes
	EnabledActiveIndicatorThickness unit.Dp
	EnabledTrackColor               token.MatColor
	EnabledTrackShape               token.CornerShapes
	EnabledTrackThickness           unit.Dp
	EnabledStopIndicatorColor       token.MatColor
	EnabledStopIndicatorShape       token.CornerShapes
	EnabledStopIndicatorThickness   unit.Dp
	EnabledSpacing                  unit.Dp
}

func BuildTheme(gtx layout.Context) *Theme {
	materialTheme := wdk.GetMaterialTheme(gtx)
	wTheme := wdk.GetWidgetTheme[Theme](materialTheme, Namespace)
	if wTheme != nil {
		return wTheme
	}
	widgetTheme := &Theme{
		EnabledActiveIndicatorColor: materialTheme.Scheme.Primary.Color,
		EnabledActiveIndicatorShape: token.UniformCornerShapes(token.CornerShape{
			Kind:        token.CornerKindRound,
			AdaptToSize: true,
		}),
		EnabledActiveIndicatorThickness: unit.Dp(4),
		EnabledTrackColor:               materialTheme.Scheme.SecondaryContainer.Color,
		EnabledTrackShape: token.UniformCornerShapes(token.CornerShape{
			Kind:        token.CornerKindRound,
			AdaptToSize: true,
		}),
		EnabledTrackThickness:     unit.Dp(4),
		EnabledStopIndicatorColor: materialTheme.Scheme.Primary.Color,
		EnabledStopIndicatorShape: token.UniformCornerShapes(token.CornerShape{
			Kind:        token.CornerKindRound,
			AdaptToSize: true,
		}),
		// Material 3 ends the track with a dot of 4 dp; the messenger's
		// bars end without one.
		EnabledStopIndicatorThickness: 0,
		EnabledSpacing:                unit.Dp(4),
	}
	wdk.InitWidgetThemeInTheme(materialTheme, Namespace, widgetTheme)
	return widgetTheme
}
