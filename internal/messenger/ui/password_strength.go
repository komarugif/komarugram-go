// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strconv"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/passwordstrength"
)

const (
	strengthBarHeight = unit.Dp(4)
	strengthBarGap    = unit.Dp(4)
)

// strengthColors are the colors of the segments for each level, from red to
// green, for the light and for the dark themes: lighter on the dark one,
// where the others would not be seen. The theme has no such colors, as it has
// none for status; the code's palettes (code_colors.go) are made the same way.
var strengthColors = [2][passwordstrength.Levels]token.MatColor{
	{
		token.NewMatColorFromHexRGB(0xD93025), token.NewMatColorFromHexRGB(0xE8710A), token.NewMatColorFromHexRGB(0xE0A100),
		token.NewMatColorFromHexRGB(0x7CB342), token.NewMatColorFromHexRGB(0x2E9E4F),
	},
	{
		token.NewMatColorFromHexRGB(0xF28B82), token.NewMatColorFromHexRGB(0xF6A15B), token.NewMatColorFromHexRGB(0xF5CB5C),
		token.NewMatColorFromHexRGB(0xA5D06B), token.NewMatColorFromHexRGB(0x6FCF8E),
	},
}

// strengthMeter shows how strong a password being typed is, in segments and
// in words, in the color of its level. It only shows it: a weak password is not
// an error, and nothing is held back for it.
type strengthMeter struct {
	password string
	result   passwordstrength.Result
}

// Layout draws the meter of password: its segments, as many as its level
// and one more, and what its level is called under them, or
// what it is a meter of while nothing is typed.
func (m *strengthMeter) Layout(gtx layout.Context, l localization.Catalog, password string) layout.Dimensions {
	if password != m.password {
		m.password, m.result = password, passwordstrength.Estimate(password)
	}
	sc := scheme(gtx)
	palette := strengthColors[0]
	if isDark(gtx) {
		palette = strengthColors[1]
	}
	filled := 0
	text := l.T("security.strength_none")
	if password != "" {
		filled = m.result.Level + 1
		text = l.T("security.strength_" + strconv.Itoa(m.result.Level))
	}
	return layout.Inset{Left: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				height := gtx.Dp(strengthBarHeight)
				gap := gtx.Dp(strengthBarGap)
				width := gtx.Constraints.Max.X
				segment := max((width-gap*(passwordstrength.Levels-1))/passwordstrength.Levels, 1)
				for i := range passwordstrength.Levels {
					color := sc.SurfaceContainerHighest
					if i < filled {
						color = palette[m.result.Level]
					}
					offset(gtx, image.Pt(i*(segment+gap), 0), func(gtx layout.Context) layout.Dimensions {
						fillRounded(gtx, color, image.Pt(segment, height), height/2)
						return layout.Dimensions{Size: image.Pt(segment, height)}
					})
				}
				return layout.Dimensions{Size: image.Pt(width, height)}
			}),
			vspace(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, text, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
			}),
		)
	})
}
