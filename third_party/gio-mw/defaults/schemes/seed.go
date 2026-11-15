// SPDX-License-Identifier: Unlicense OR MIT

package schemes

import (
	"image/color"
	"math"

	"gio-mw/token"
)

// SchemeFromSeed makes the Material 3 scheme of a seed color, light or
// dark, as the Material Theme Builder makes the schemes of this package
// (Baseline is the one of #5F26D2): the "tonal spot" variant, whose
// primary has the seed's hue and a moderate chroma, whatever the seed's
// own, and whose other palettes are made from that hue.
func SchemeFromSeed(seed color.NRGBA, dark bool) *token.Scheme {
	return schemeFromSeed(seed, dark, false)
}

// BaselineSeed is the seed of Baseline.
var BaselineSeed = color.NRGBA{R: 0x5F, G: 0x26, B: 0xD2, A: 255}

// SchemeFromSeedNeutral is SchemeFromSeed with neutral surfaces: the
// surfaces, the background, the outlines and their colors are grays, not
// tinted with the seed's hue; the primary, secondary and tertiary colors
// are the same.
func SchemeFromSeedNeutral(seed color.NRGBA, dark bool) *token.Scheme {
	return schemeFromSeed(seed, dark, true)
}

func schemeFromSeed(seed color.NRGBA, dark, neutral bool) *token.Scheme {
	hue, _, _ := hctOf(seed)
	p := tonalPalette{hue, 36}
	s := tonalPalette{hue, 16}
	t := tonalPalette{math.Mod(hue+60, 360), 24}
	n := tonalPalette{hue, 6}
	nv := tonalPalette{hue, 8}
	if neutral {
		n.chroma, nv.chroma = 0, 0
	}
	e := tonalPalette{25, 84}

	// The tones of each role, light and dark.
	pick := func(light, darkTone float64) float64 {
		if dark {
			return darkTone
		}
		return light
	}
	set := func(pal tonalPalette, color, on [2]float64) token.MatColorSet {
		return token.MatColorSet{
			Color:   pal.tone(pick(color[0], color[1])),
			OnColor: pal.tone(pick(on[0], on[1])),
		}
	}
	one := func(pal tonalPalette, tones [2]float64) token.MatColor {
		return pal.tone(pick(tones[0], tones[1]))
	}
	fixed := func(pal tonalPalette) (token.MatColorSet, token.MatColorSet) {
		return token.MatColorSet{Color: pal.tone(90), OnColor: pal.tone(10)},
			token.MatColorSet{Color: pal.tone(80), OnColor: pal.tone(30)}
	}
	var sc token.Scheme
	sc.Primary = set(p, [2]float64{40, 80}, [2]float64{100, 20})
	sc.PrimaryContainer = set(p, [2]float64{90, 30}, [2]float64{30, 90})
	sc.PrimaryFixed, sc.PrimaryFixedVariant = fixed(p)
	sc.Secondary = set(s, [2]float64{40, 80}, [2]float64{100, 20})
	sc.SecondaryContainer = set(s, [2]float64{90, 30}, [2]float64{30, 90})
	sc.SecondaryFixed, sc.SecondaryFixedVariant = fixed(s)
	sc.Tertiary = set(t, [2]float64{40, 80}, [2]float64{100, 20})
	sc.TertiaryContainer = set(t, [2]float64{90, 30}, [2]float64{30, 90})
	sc.TertiaryFixed, sc.TertiaryFixedVariant = fixed(t)
	sc.Surface = set(n, [2]float64{98, 6}, [2]float64{10, 90})
	sc.SurfaceVariant = set(nv, [2]float64{90, 30}, [2]float64{30, 80})
	sc.SurfaceDim = one(n, [2]float64{87, 6})
	sc.SurfaceBright = one(n, [2]float64{98, 24})
	sc.SurfaceContainerLowest = one(n, [2]float64{100, 4})
	sc.SurfaceContainerLow = one(n, [2]float64{96, 10})
	sc.SurfaceContainer = one(n, [2]float64{94, 12})
	sc.SurfaceContainerHigh = one(n, [2]float64{92, 17})
	sc.SurfaceContainerHighest = one(n, [2]float64{90, 22})
	sc.InverseSurface = set(n, [2]float64{20, 90}, [2]float64{95, 20})
	sc.InversePrimary = one(p, [2]float64{80, 40})
	sc.Background = set(n, [2]float64{98, 6}, [2]float64{10, 90})
	sc.Outline = one(nv, [2]float64{50, 60})
	sc.OutlineVariant = one(nv, [2]float64{80, 30})
	sc.Error = set(e, [2]float64{40, 80}, [2]float64{100, 20})
	sc.ErrorContainer = set(e, [2]float64{90, 30}, [2]float64{30, 90})
	sc.Scrim = n.tone(0)
	sc.Shadow = n.tone(0)
	return &sc
}

// tonalPalette is the colors of one hue and chroma, by tone.
type tonalPalette struct {
	hue, chroma float64
}

func (p tonalPalette) tone(t float64) token.MatColor {
	return token.MatColor(colorOfHCT(p.hue, p.chroma, t))
}
