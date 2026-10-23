// SPDX-License-Identifier: Unlicense OR MIT

// Package countries knows the countries' calling codes: which country a
// phone number being typed belongs to, and how to group its digits so that it
// is easy to read and to check against the one the user has in mind.
//
// The names of the countries are not here: they are the interface's, keys
// `country.<ISO>` of the localization files.
package countries

import (
	"strings"
	"sync"
)

// MaxDigits is how many digits a phone number has at most, the country's
// calling code included (ITU-T E.164).
const MaxDigits = 15

// Country is a country, or a territory with a calling code of its own.
type Country struct {
	// ISO is the country's code in ISO 3166-1 alpha-2, "XK" for Kosovo.
	ISO string
	// Dial is the calling code, in digits, without the plus.
	Dial string
	// Prefixes are the digits after Dial that make a number this country's
	// where Dial is shared: 242 is the Bahamas' in +1. A country without
	// them is the default of its Dial.
	Prefixes []string
	// groups are the sizes of the groups of the digits after Dial.
	groups []int
}

// Code is how the calling code is written in a list of countries.
func (c Country) Code() string {
	return "+" + c.Dial + c.shownPrefix()
}

// shownPrefix is the area code written after the calling code, when the
// country has a few of them, as the Dominican Republic does, not a dozen.
func (c Country) shownPrefix() string {
	if len(c.Prefixes) == 0 || len(c.Prefixes) > 3 || len(c.Prefixes[0]) < 3 {
		return ""
	}
	return " " + c.Prefixes[0]
}

// Digits is the digits a number starts with in this country: its calling
// code, and the area code if it is shown.
func (c Country) Digits() string {
	return Digits(c.Code())
}

// table is the countries, a line each: the ISO code, the calling code with
// the digits that tell the country from others of the code after a colon,
// and the sizes of the groups of the rest of the number, an X for a digit.
// The groupings are how the numbers are written in the country, as well as a
// table of this size knows.
const table = `
AF 93 XX XXX XXXX
AL 355 XX XXX XXXX
DZ 213 XXX XX XX XX
AS 1:684 XXX XXX XXXX
AD 376 XXX XXX
AO 244 XXX XXX XXX
AI 1:264 XXX XXX XXXX
AG 1:268 XXX XXX XXXX
AR 54 XX XXXX XXXX
AM 374 XX XXX XXX
AW 297 XXX XXXX
AU 61 XXX XXX XXX
AT 43 XXX XXXXXXXX
AZ 994 XX XXX XX XX
BS 1:242 XXX XXX XXXX
BH 973 XXXX XXXX
BD 880 XXXX XXXXXX
BB 1:246 XXX XXX XXXX
BY 375 XX XXX XX XX
BE 32 XXX XX XX XX
BZ 501 XXX XXXX
BJ 229 XX XX XX XX
BM 1:441 XXX XXX XXXX
BT 975 XX XXX XXX
BO 591 XXXX XXXX
BA 387 XX XXX XXX
BW 267 XX XXX XXX
BR 55 XX XXXXX XXXX
IO 246 XXX XXXX
VG 1:284 XXX XXX XXXX
BN 673 XXX XXXX
BG 359 XX XXX XXXX
BF 226 XX XX XX XX
BI 257 XX XX XX XX
KH 855 XX XXX XXX
CM 237 X XX XX XX XX
CA 1:204,226,236,249,250,289,306,343,365,367,368,382,403,416,418,431,437,438,450,468,474,506,514,519,548,579,581,584,587,604,613,639,647,672,705,709,742,778,780,782,807,819,825,867,873,879,902,905 XXX XXX XXXX
CV 238 XXX XX XX
KY 1:345 XXX XXX XXXX
CF 236 XX XX XX XX
TD 235 XX XX XX XX
CL 56 X XXXX XXXX
CN 86 XXX XXXX XXXX
CO 57 XXX XXX XXXX
KM 269 XXX XX XX
CG 242 XX XXX XXXX
CD 243 XXX XXX XXX
CK 682 XX XXX
CR 506 XXXX XXXX
CI 225 XX XX XX XX XX
HR 385 XX XXX XXXX
CU 53 X XXX XXXX
CW 599 X XXX XXXX
CY 357 XX XXXXXX
CZ 420 XXX XXX XXX
DK 45 XX XX XX XX
DJ 253 XX XX XX XX
DM 1:767 XXX XXX XXXX
DO 1:809,829,849 XXX XXX XXXX
EC 593 XX XXX XXXX
EG 20 XXX XXX XXXX
SV 503 XXXX XXXX
GQ 240 XXX XXX XXX
ER 291 X XXX XXX
EE 372 XXXX XXXX
ET 251 XX XXX XXXX
FK 500 XXXXX
FO 298 XXX XXX
FJ 679 XXX XXXX
FI 358 XX XXX XXXX
FR 33 X XX XX XX XX
GF 594 XXX XX XX XX
PF 689 XX XX XX XX
GA 241 X XX XX XX
GM 220 XXX XXXX
GE 995 XXX XX XX XX
DE 49 XXX XXXXXXXX
GH 233 XX XXX XXXX
GI 350 XXXX XXXX
GR 30 XXX XXX XXXX
GL 299 XX XX XX
GD 1:473 XXX XXX XXXX
GP 590 XXX XX XX XX
GU 1:671 XXX XXX XXXX
GT 502 XXXX XXXX
GN 224 XXX XX XX XX
GW 245 XXX XXXXXX
GY 592 XXX XXXX
HT 509 XX XX XXXX
HN 504 XXXX XXXX
HK 852 XXXX XXXX
HU 36 XX XXX XXXX
IS 354 XXX XXXX
IN 91 XXXXX XXXXX
ID 62 XXX XXXX XXXX
IR 98 XXX XXX XXXX
IQ 964 XXX XXX XXXX
IE 353 XX XXX XXXX
IL 972 XX XXX XXXX
IT 39 XXX XXX XXXX
JM 1:876 XXX XXX XXXX
JP 81 XX XXXX XXXX
JO 962 X XXXX XXXX
KZ 7:6,7 XXX XXX XX XX
KE 254 XXX XXXXXX
KI 686 XXXXXXXX
XK 383 XX XXX XXX
KW 965 XXXX XXXX
KG 996 XXX XXX XXX
LA 856 XX XX XXX XXX
LV 371 XX XXX XXX
LB 961 XX XXX XXX
LS 266 XXXX XXXX
LR 231 XX XXX XXXX
LY 218 XX XXX XXXX
LI 423 XXX XXX XXX
LT 370 XXX XXXXX
LU 352 XXX XXX XXX
MO 853 XXXX XXXX
MK 389 XX XXX XXX
MG 261 XX XX XXX XX
MW 265 XXX XX XX XX
MY 60 XX XXXX XXXX
MV 960 XXX XXXX
ML 223 XX XX XX XX
MT 356 XXXX XXXX
MH 692 XXX XXXX
MQ 596 XXX XX XX XX
MR 222 XX XX XX XX
MU 230 XXXX XXXX
RE 262 XXX XX XX XX
YT 262:269,639 XXX XX XX XX
MX 52 XXX XXX XXXX
FM 691 XXX XXXX
MD 373 XXXX XXXX
MC 377 XX XX XX XX
MN 976 XX XX XXXX
ME 382 XX XXX XXX
MS 1:664 XXX XXX XXXX
MA 212 XXX XXXXXX
MZ 258 XX XXX XXXX
MM 95 XX XXX XXXX
NA 264 XX XXX XXXX
NR 674 XXX XXXX
NP 977 XX XXXX XXXX
NL 31 X XX XX XX XX
NC 687 XX XX XX
NZ 64 XX XXX XXXX
NI 505 XXXX XXXX
NE 227 XX XX XX XX
NG 234 XXX XXX XXXX
NU 683 XXXX
NF 672 X XXXXX
KP 850 XXX XXX XXXX
MP 1:670 XXX XXX XXXX
NO 47 XXX XX XXX
OM 968 XXXX XXXX
PK 92 XXX XXXXXXX
PW 680 XXX XXXX
PS 970 XXX XXX XXX
PA 507 XXXX XXXX
PG 675 XXX XXXXX
PY 595 XXX XXXXXX
PE 51 XXX XXX XXX
PH 63 XXX XXX XXXX
PL 48 XXX XXX XXX
PT 351 XXX XXX XXX
PR 1:787,939 XXX XXX XXXX
QA 974 XXXX XXXX
RO 40 XXX XXX XXX
RU 7 XXX XXX XX XX
RW 250 XXX XXX XXX
WS 685 XX XXXXX
SM 378 XXXX XXXXXX
ST 239 XXX XXXX
SA 966 XX XXX XXXX
SN 221 XX XXX XX XX
RS 381 XX XXX XXXX
SC 248 X XXX XXX
SL 232 XX XXXXXX
SG 65 XXXX XXXX
SX 1:721 XXX XXX XXXX
SK 421 XXX XXX XXX
SI 386 XX XXX XXX
SB 677 XXXXX
SO 252 XX XXX XXX
ZA 27 XX XXX XXXX
KR 82 XX XXXX XXXX
SS 211 XXX XXX XXX
ES 34 XXX XXX XXX
LK 94 XX XXX XXXX
SH 290 XXXX
KN 1:869 XXX XXX XXXX
LC 1:758 XXX XXX XXXX
PM 508 XX XX XX
VC 1:784 XXX XXX XXXX
SD 249 XX XXX XXXX
SR 597 XXX XXXX
SZ 268 XXXX XXXX
SE 46 XX XXX XX XX
CH 41 XX XXX XXXX
SY 963 XXX XXX XXX
TW 886 XXX XXX XXX
TJ 992 XX XXX XXXX
TZ 255 XX XXX XXXX
TH 66 XX XXX XXXX
TL 670 XXXX XXXX
TG 228 XX XX XX XX
TK 690 XXXX
TO 676 XXXXX
TT 1:868 XXX XXX XXXX
TN 216 XX XXX XXX
TR 90 XXX XXX XX XX
TM 993 XX XXXXXX
TC 1:649 XXX XXX XXXX
TV 688 XXXXX
UG 256 XXX XXXXXX
UA 380 XX XXX XXXX
AE 971 XX XXX XXXX
GB 44 XXXX XXXXXX
US 1 XXX XXX XXXX
VI 1:340 XXX XXX XXXX
UY 598 XX XXX XXX
UZ 998 XX XXX XXXX
VU 678 XXXXX
VE 58 XXX XXX XXXX
VN 84 XX XXXX XXXX
WF 681 XX XX XX
YE 967 XXX XXX XXX
ZM 260 XX XXX XXXX
ZW 263 XX XXX XXXX
`

var (
	loadOnce sync.Once
	all      []Country
	byDial   map[string][]int
	byISO    map[string]int
)

func load() {
	loadOnce.Do(func() {
		byDial, byISO = map[string][]int{}, map[string]int{}
		for _, line := range strings.Split(table, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			c := Country{ISO: fields[0]}
			dial, prefixes, _ := strings.Cut(fields[1], ":")
			c.Dial = dial
			if prefixes != "" {
				c.Prefixes = strings.Split(prefixes, ",")
			}
			for _, group := range fields[2:] {
				c.groups = append(c.groups, len(group))
			}
			byISO[c.ISO] = len(all)
			byDial[c.Dial] = append(byDial[c.Dial], len(all))
			all = append(all, c)
		}
	})
}

// All returns every country, by ISO code in the order of the table.
func All() []Country {
	load()
	return append([]Country(nil), all...)
}

// ByISO returns the country with the ISO code.
func ByISO(iso string) (Country, bool) {
	load()
	i, ok := byISO[iso]
	if !ok {
		return Country{}, false
	}
	return all[i], true
}

// Digits returns the digits of s, whatever else it has.
func Digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Match returns the country that the number starting with digits is in, the
// number being typed as well. Where a calling code is shared the country is
// found by the digits after it; of those that do not tell, chosen, an ISO
// code, if it has the code, and then the code's default are taken. It fails
// when no calling code is a start of digits.
func Match(digits, chosen string) (Country, bool) {
	load()
	for n := 1; n <= 4 && n <= len(digits); n++ {
		list := byDial[digits[:n]]
		if len(list) == 0 {
			continue
		}
		rest := digits[n:]
		best, bestLen := -1, 0
		fallback, picked := -1, -1
		for _, i := range list {
			c := all[i]
			if len(c.Prefixes) == 0 {
				fallback = i
			}
			if c.ISO == chosen {
				picked = i
			}
			for _, p := range c.Prefixes {
				if len(p) > bestLen && strings.HasPrefix(rest, p) {
					best, bestLen = i, len(p)
				}
			}
		}
		switch {
		case best >= 0:
			return all[best], true
		case picked >= 0:
			return all[picked], true
		case fallback >= 0:
			return all[fallback], true
		}
		return all[list[0]], true
	}
	return Country{}, false
}

// Format writes the digits of a number as it is read: the plus, the calling
// code and the rest in the groups of the country, apart by spaces, "+7 900
// 123 45 67". Digits past a group's size, or of a code that is not known,
// follow as they are. No separator ends it: a separator comes with the digit
// that follows it. Digits past MaxDigits are left out.
func Format(digits string) string {
	if digits == "" {
		return ""
	}
	if len(digits) > MaxDigits {
		digits = digits[:MaxDigits]
	}
	c, ok := Match(digits, "")
	if !ok {
		return "+" + digits
	}
	rest := digits[len(c.Dial):]
	var b strings.Builder
	b.WriteString("+" + c.Dial)
	for i := 0; rest != ""; i++ {
		b.WriteByte(' ')
		size := len(rest)
		if i < len(c.groups) {
			size = min(c.groups[i], len(rest))
		}
		// Digits past the last group join it.
		if i == len(c.groups)-1 {
			size = len(rest)
		}
		b.WriteString(rest[:size])
		rest = rest[size:]
	}
	return b.String()
}

// Flag returns the country's flag as an emoji: the two regional indicator
// letters of its ISO code, which a font with flags draws as one picture.
func (c Country) Flag() string {
	if len(c.ISO) != 2 {
		return ""
	}
	return string([]rune{0x1F1E6 + rune(c.ISO[0]-'A'), 0x1F1E6 + rune(c.ISO[1]-'A')})
}
