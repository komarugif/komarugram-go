// SPDX-License-Identifier: Unlicense OR MIT

package localization

import "strings"

// A pluralRule names the plural form of a whole number in a language as
// CLDR does: "zero", "one", "two", "few", "many" or "other". Telegram's
// language packs name their forms the same way.
type pluralRule func(n int) string

// pluralFor is the rule of a language, by the code a pack names it with
// (Telegram's plural_code), or by the language's own; a code it does not
// know counts as English does. The rules are CLDR's cardinal ones for
// whole numbers (https://www.unicode.org/cldr/charts/latest/supplemental/language_plural_rules.html).
func pluralFor(code string) pluralRule {
	code = strings.ToLower(code)
	if rule, ok := pluralRules[code]; ok {
		return rule
	}
	if primary, _, found := strings.Cut(code, "-"); found {
		if rule, ok := pluralRules[primary]; ok {
			return rule
		}
	}
	return pluralOne
}

var pluralRules = map[string]pluralRule{}

func init() {
	for rule, codes := range map[*pluralRule]string{
		&pluralNone:       "ja ko zh id ms th vi lo my km jv su yo ig bo dz",
		&pluralZeroOrOne:  "fr pt hi fa bn gu kn mr zu am hy si ln ti wa",
		&pluralEastSlavic: "ru uk be",
		&pluralPolish:     "pl",
		&pluralCzech:      "cs sk",
		&pluralSerbian:    "hr sr bs sh",
		&pluralSlovenian:  "sl",
		&pluralLithuanian: "lt",
		&pluralLatvian:    "lv",
		&pluralRomanian:   "ro mo",
		&pluralArabic:     "ar",
		&pluralHebrew:     "he iw",
		&pluralIrish:      "ga",
		&pluralWelsh:      "cy",
	} {
		for _, code := range strings.Fields(codes) {
			pluralRules[code] = *rule
		}
	}
}

var (
	pluralOne pluralRule = func(n int) string {
		if n == 1 {
			return "one"
		}
		return "other"
	}
	pluralNone      pluralRule = func(int) string { return "other" }
	pluralZeroOrOne pluralRule = func(n int) string {
		if n == 0 || n == 1 {
			return "one"
		}
		return "other"
	}
	pluralEastSlavic pluralRule = func(n int) string {
		switch n10, n100 := n%10, n%100; {
		case n10 == 1 && n100 != 11:
			return "one"
		case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
			return "few"
		}
		return "many"
	}
	pluralPolish pluralRule = func(n int) string {
		switch n10, n100 := n%10, n%100; {
		case n == 1:
			return "one"
		case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
			return "few"
		}
		return "many"
	}
	pluralCzech pluralRule = func(n int) string {
		switch {
		case n == 1:
			return "one"
		case n >= 2 && n <= 4:
			return "few"
		}
		return "other"
	}
	pluralSerbian pluralRule = func(n int) string {
		switch n10, n100 := n%10, n%100; {
		case n10 == 1 && n100 != 11:
			return "one"
		case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
			return "few"
		}
		return "other"
	}
	pluralSlovenian pluralRule = func(n int) string {
		switch n % 100 {
		case 1:
			return "one"
		case 2:
			return "two"
		case 3, 4:
			return "few"
		}
		return "other"
	}
	pluralLithuanian pluralRule = func(n int) string {
		switch n10, n100 := n%10, n%100; {
		case n100 >= 11 && n100 <= 19:
			return "other"
		case n10 == 1:
			return "one"
		case n10 >= 2:
			return "few"
		}
		return "other"
	}
	pluralLatvian pluralRule = func(n int) string {
		switch n10, n100 := n%10, n%100; {
		case n10 == 0 || n100 >= 11 && n100 <= 19:
			return "zero"
		case n10 == 1:
			return "one"
		}
		return "other"
	}
	pluralRomanian pluralRule = func(n int) string {
		switch n100 := n % 100; {
		case n == 1:
			return "one"
		case n == 0 || n100 >= 2 && n100 <= 19:
			return "few"
		}
		return "other"
	}
	pluralArabic pluralRule = func(n int) string {
		switch n100 := n % 100; {
		case n == 0:
			return "zero"
		case n == 1:
			return "one"
		case n == 2:
			return "two"
		case n100 >= 3 && n100 <= 10:
			return "few"
		case n100 >= 11:
			return "many"
		}
		return "other"
	}
	pluralHebrew pluralRule = func(n int) string {
		switch n {
		case 1:
			return "one"
		case 2:
			return "two"
		}
		return "other"
	}
	pluralIrish pluralRule = func(n int) string {
		switch {
		case n == 1:
			return "one"
		case n == 2:
			return "two"
		case n >= 3 && n <= 6:
			return "few"
		case n >= 7 && n <= 10:
			return "many"
		}
		return "other"
	}
	pluralWelsh pluralRule = func(n int) string {
		switch n {
		case 0:
			return "zero"
		case 1:
			return "one"
		case 2:
			return "two"
		case 3:
			return "few"
		case 6:
			return "many"
		}
		return "other"
	}
)
