// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux && !android

package appearance

import (
	"bufio"
	"image/color"
	"strings"
	"testing"
)

func TestKDEAccent(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		want color.NRGBA
		ok   bool
	}{
		"applied":     {"[Colors:Selection]\nBackgroundNormal=61,174,233\n[General]\nAccentColor=233,100,10\n", color.NRGBA{61, 174, 233, 255}, true},
		"accent only": {"[General]\nAccentColor=233,100,10\n", color.NRGBA{233, 100, 10, 255}, true},
		"selection":   {"[Colors:Selection]\nBackgroundNormal=61,174,233\n", color.NRGBA{61, 174, 233, 255}, true},
		"hex":         {"[General]\nAccentColor=#e9640a\n", color.NRGBA{233, 100, 10, 255}, true},
		"other group": {"[Colors:View]\nBackgroundNormal=1,2,3\n", color.NRGBA{}, false},
	} {
		got, ok := parseKDEAccent(bufio.NewScanner(strings.NewReader(tc.file)))
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got %v %v, want %v %v", name, got, ok, tc.want, tc.ok)
		}
	}
}
