// SPDX-License-Identifier: Unlicense OR MIT

package schemes

import (
	"encoding/json"
	"image/color"
	"testing"

	"gio-mw/token"
)

// The scheme of Baseline's seed is Baseline, as the Theme Builder made it,
// within a step or two of a channel.
func TestSchemeFromSeedIsBaseline(t *testing.T) {
	seed := BaselineSeed
	for _, tc := range []struct {
		name string
		dark bool
		want *token.Scheme
	}{{"light", false, SchemeBaselineLight()}, {"dark", true, SchemeBaselineDark()}} {
		got := flatten(t, SchemeFromSeed(seed, tc.dark))
		want := flatten(t, tc.want)
		for role, w := range want {
			g := got[role]
			if d := maxDiff(g, w); d > 2 {
				t.Errorf("%s %s: got %v, want %v", tc.name, role, g, w)
			}
		}
	}
}

func flatten(t *testing.T, s *token.Scheme) map[string]color.NRGBA {
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(b, &raw)
	out := make(map[string]color.NRGBA)
	for k, v := range raw {
		var set struct{ Color, OnColor *color.NRGBA }
		if json.Unmarshal(v, &set) == nil && set.Color != nil {
			out[k] = *set.Color
			out[k+".On"] = *set.OnColor
			continue
		}
		var c color.NRGBA
		json.Unmarshal(v, &c)
		out[k] = c
	}
	return out
}

func maxDiff(a, b color.NRGBA) int {
	d := 0
	for _, p := range [][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}, {a.A, b.A}} {
		x := int(p[0]) - int(p[1])
		if x < 0 {
			x = -x
		}
		d = max(d, x)
	}
	return d
}

// Neutral surfaces are grays, and the accent's colors stay.
func TestSchemeFromSeedNeutral(t *testing.T) {
	orange := color.NRGBA{R: 233, G: 100, B: 10, A: 255}
	for _, dark := range []bool{false, true} {
		got, tinted := SchemeFromSeedNeutral(orange, dark), SchemeFromSeed(orange, dark)
		for _, c := range []token.MatColor{got.Surface.Color, got.Surface.OnColor, got.SurfaceContainer, got.SurfaceVariant.Color, got.Background.Color, got.Outline, got.InverseSurface.Color} {
			if c.R != c.G || c.G != c.B {
				t.Errorf("dark %v: surface color %v is not gray", dark, c)
			}
		}
		if got.Primary != tinted.Primary || got.SecondaryContainer != tinted.SecondaryContainer {
			t.Errorf("dark %v: the accent's colors changed", dark)
		}
	}
}
