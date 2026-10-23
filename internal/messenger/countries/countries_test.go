// SPDX-License-Identifier: Unlicense OR MIT

package countries

import (
	"strings"
	"testing"
)

func TestTableIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	defaults := map[string]int{}
	dials := map[string]bool{}
	for _, c := range All() {
		if len(c.ISO) != 2 || c.ISO != strings.ToUpper(c.ISO) {
			t.Errorf("ISO code %q", c.ISO)
		}
		if seen[c.ISO] {
			t.Errorf("%s twice", c.ISO)
		}
		seen[c.ISO] = true
		if c.Dial == "" || Digits(c.Dial) != c.Dial || len(c.Dial) > 4 {
			t.Errorf("%s: calling code %q", c.ISO, c.Dial)
		}
		if len(c.groups) == 0 {
			t.Errorf("%s has no groups", c.ISO)
		}
		if len(c.Prefixes) == 0 {
			defaults[c.Dial]++
		}
		dials[c.Dial] = true
	}
	for dial := range dials {
		// Every code has the country its numbers are taken to be in.
		if defaults[dial] != 1 {
			t.Errorf("+%s has %d default countries, want 1", dial, defaults[dial])
		}
		// No code starts another, or a number could be read in two ways.
		for other := range dials {
			if other != dial && strings.HasPrefix(other, dial) {
				t.Errorf("+%s starts +%s", other, dial)
			}
		}
	}
	// No area code is another's start: the longest would never be the one.
	for _, c := range All() {
		for _, p := range c.Prefixes {
			for _, d := range All() {
				for _, q := range d.Prefixes {
					if c.Dial == d.Dial && p != q && strings.HasPrefix(q, p) {
						t.Errorf("%s's %s starts %s's %s", d.ISO, q, c.ISO, p)
					}
				}
			}
		}
	}
}

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		digits, chosen, want string
	}{
		{"7", "", "RU"},
		{"7900123", "", "RU"},
		{"77012345678", "", "KZ"},
		{"76", "", "KZ"},
		{"1", "", "US"},
		{"12025550123", "", "US"},
		{"14165550123", "", "CA"},
		{"12423571234", "", "BS"},
		{"1809", "", "DO"},
		{"44", "", "GB"},
		{"380", "", "UA"},
		{"262269", "", "YT"},
		{"262692", "", "RE"},
		// A country picked by the user stays while its code has no better.
		{"1", "CA", "CA"},
		{"14", "CA", "CA"},
		{"1242", "CA", "BS"},
		{"7", "KZ", "KZ"},
		// But not under another code.
		{"44", "KZ", "GB"},
	} {
		got, ok := Match(tc.digits, tc.chosen)
		if !ok || got.ISO != tc.want {
			t.Errorf("Match(%q, %q) = %q, %v, want %s", tc.digits, tc.chosen, got.ISO, ok, tc.want)
		}
	}
	for _, digits := range []string{"", "0", "999", "8"} {
		if c, ok := Match(digits, ""); ok {
			t.Errorf("Match(%q) = %s, want none", digits, c.ISO)
		}
	}
}

func TestFormat(t *testing.T) {
	for digits, want := range map[string]string{
		"":              "",
		"7":             "+7",
		"79":            "+7 9",
		"7900":          "+7 900",
		"79001":         "+7 900 1",
		"7900123":       "+7 900 123",
		"79001234":      "+7 900 123 4",
		"790012345":     "+7 900 123 45",
		"79001234567":   "+7 900 123 45 67",
		"12345678910":   "+1 234 567 8910",
		"380501234567":  "+380 50 123 4567",
		"4915112345678": "+49 151 12345678",
		// Digits past the last group follow it.
		"790012345678": "+7 900 123 45 678",
		// A code that is not known leaves the digits alone.
		"999123": "+999123",
		// A number has at most MaxDigits.
		"7900123456789012345": "+7 900 123 45 678901",
	} {
		if got := Format(digits); got != want {
			t.Errorf("Format(%q) = %q, want %q", digits, got, want)
		}
	}
}

func TestFormatNeverEndsWithASeparator(t *testing.T) {
	for _, c := range All() {
		digits := c.Digits() + "1234567890123"
		for n := 1; n <= len(digits); n++ {
			text := Format(digits[:n])
			if strings.HasSuffix(text, " ") {
				t.Fatalf("Format(%q) = %q ends with a space", digits[:n], text)
			}
			if Digits(text) != digits[:min(n, MaxDigits)] {
				t.Fatalf("Format(%q) = %q changes the digits", digits[:n], text)
			}
		}
	}
}

func TestCode(t *testing.T) {
	for iso, want := range map[string]string{"RU": "+7", "KZ": "+7", "US": "+1", "CA": "+1", "BS": "+1 242", "DO": "+1 809", "PR": "+1 787", "DE": "+49"} {
		c, ok := ByISO(iso)
		if !ok || c.Code() != want {
			t.Errorf("%s: Code() = %q, want %q", iso, c.Code(), want)
		}
	}
	if _, ok := ByISO("ZZ"); ok {
		t.Error("ZZ is a country")
	}
}

func TestFlag(t *testing.T) {
	for iso, want := range map[string]string{"RU": "\U0001F1F7\U0001F1FA", "US": "\U0001F1FA\U0001F1F8", "XK": "\U0001F1FD\U0001F1F0"} {
		c, _ := ByISO(iso)
		if got := c.Flag(); got != want {
			t.Errorf("%s: flag %q, want %q", iso, got, want)
		}
	}
	// Every country's flag is its two letters, and no other emoji.
	for _, c := range All() {
		flag := []rune(c.Flag())
		if len(flag) != 2 || flag[0] < 0x1F1E6 || flag[0] > 0x1F1FF || flag[1] < 0x1F1E6 || flag[1] > 0x1F1FF {
			t.Errorf("%s: flag %q", c.ISO, c.Flag())
		}
	}
}
