// SPDX-License-Identifier: Unlicense OR MIT

package passwordstrength

import (
	"strings"
	"testing"
)

func TestCommonPasswordsAreLow(t *testing.T) {
	for _, password := range []string{
		"password", "123456", "qwerty", "qwerty123", "Password1", "P@ssw0rd", "iloveyou",
		"11111111", "aaaaaaaa", "abcdefgh", "12345678", "asdfghjk", "1q2w3e4r",
		// Words, names and years.
		"monkey123", "Anna1990", "Dragon2024", "Sergey1985", "Moscow2020",
		// In Russian, and in the English layout for it.
		"привет", "пароль123", "Любовь1", "ghbdtn", "ghbdtn123", "Привет2020",
	} {
		if got := Estimate(password); got.Level > 1 {
			t.Errorf("%q: level %d (%.1f bits), want at most 1", password, got.Level, got.Bits)
		}
	}
}

func TestUniquePasswordsAreHigh(t *testing.T) {
	for password, least := range map[string]int{
		// The letters are no words and no pattern: four of them are as many as
		// they are, ten are plenty, and the highest level is for those that have
		// the usual kinds of characters too.
		"x7#kQ9$mP2vL": 4,
		"Rj4!pZw8&nQe": 4,
		"kQ7#vmZp":     4,
		"vkqzmwpdhx":   3,
		"0xk3Qz9Lw":    3,
		"vkqzmwpd":     3,
		"vkqzmw":       2,
		"vkqz":         1,
		// Words in a row that no dictionary has are not asked for, but they
		// are not low either.
		"correct horse battery staple":    3,
		"lantern violin harvest":          3,
		"Correct horse battery staple 7!": 4,
	} {
		if got := Estimate(password); got.Level < least {
			t.Errorf("%q: level %d (%.1f bits), want at least %d", password, got.Level, got.Bits, least)
		}
	}
}

// TestNotStrictAboutLength checks that the top level does not take four or
// five random words: a password of eight characters, random, is good, and
// with the usual kinds of characters, the highest, as one of ten letters is
// good.
func TestNotStrictAboutLength(t *testing.T) {
	if got := Estimate("vkqzmwpd"); got.Level != 3 {
		t.Errorf("eight random letters: level %d, want 3", got.Level)
	}
	if got := Estimate("vkqzmwpdhx"); got.Level != Levels-2 {
		t.Errorf("ten random letters: level %d, want %d", got.Level, Levels-2)
	}
	if got := Estimate("kQ7#vmZp"); got.Level != Levels-1 {
		t.Errorf("eight random characters of every kind: level %d, want %d", got.Level, Levels-1)
	}
}

func TestCommonPartsLowerTheEstimateAndUniqueOnesRaiseIt(t *testing.T) {
	more := func(a, b string) {
		t.Helper()
		if x, y := Estimate(a).Bits, Estimate(b).Bits; x <= y {
			t.Errorf("%q (%.1f bits) is not more than %q (%.1f bits)", a, x, b, y)
		}
	}
	// What is added makes it more, whatever it is added to.
	more("password7#kQ9", "password")
	more("vkqzmwpdhx", "vkqzmwpd")
	more("vkqzmwpd", "vkqzmw")
	// The same length, with a common word or a pattern in it, is less.
	more("vkqzmwpdhx", "passwordhx")
	more("vkqzmwpdhx", "qwertyuiop")
	more("vkqzmwpdhx", "1234567890")
	more("vkqzmwpdhx", "aaaaaaaaaa")
	// A word that is typed in the other layout is no stronger for it.
	more("xkqzmw", "ghbdtn")
	// Capitals and digits for letters add little to a common word.
	if d := Estimate("P4ssw0rd").Bits - Estimate("password").Bits; d > 8 {
		t.Errorf("P4ssw0rd is %.1f bits over password, want little", d)
	}
}

func TestPartsOfAPasswordAreFound(t *testing.T) {
	load()
	for _, tc := range []struct {
		password string
		bits     float64 // at most
	}{
		{"qwertyuiop", 15},
		{"abcdefghij", 12},
		{"9876543210", 12},
		{"aaaaaaaaaa", 12},
		{"abababababab", 14},
		{"19902001", 25},
		{"01011990", 25},
	} {
		if got := Estimate(tc.password).Bits; got > tc.bits {
			t.Errorf("%q: %.1f bits, want at most %.1f", tc.password, got, tc.bits)
		}
	}
}

func TestEdges(t *testing.T) {
	if got := Estimate(""); got != (Result{}) {
		t.Errorf("empty: %+v", got)
	}
	if got := Estimate("a"); got.Level != 0 || got.Bits <= 0 {
		t.Errorf("one letter: %+v", got)
	}
	// Whatever the characters, it answers, and more characters are not less.
	for _, password := range []string{"      ", "😀😀😀😀", "日本語のパスワード", "\x00\x01\x02", strings.Repeat("x", 1000), strings.Repeat("Zq9#", 500)} {
		got := Estimate(password)
		if got.Level < 0 || got.Level >= Levels || got.Bits < 0 {
			t.Errorf("%q: %+v", password, got)
		}
	}
	long := Estimate(strings.Repeat("Zq9#kM", 10))
	if long.Level != Levels-1 {
		t.Errorf("a long unique password is level %d", long.Level)
	}
}

// TestEveryCommonPasswordIsTheLowestLevel checks the list of the most common
// passwords is what the estimate is made from: each of them, as it is and
// with a capital, is a guess among the first.
func TestEveryCommonPasswordIsTheLowestLevel(t *testing.T) {
	list := strings.Fields(topPasswords)
	if len(list) != 1000 {
		t.Fatalf("%d passwords in the list, want 1000", len(list))
	}
	for _, password := range list {
		for _, p := range []string{password, strings.ToUpper(password[:1]) + password[1:]} {
			if got := Estimate(p); got.Level != 0 {
				t.Errorf("%q is among the 1000 most common passwords and is level %d (%.1f bits)", p, got.Level, got.Bits)
			}
		}
	}
}

// TestKeyMashingIsLow checks that keys pressed at random in one place of the
// keyboard, which is what a few letters in a row typed without thought are,
// do not count as unique characters: a few keys of one place are a few bits
// each, not what a key of the whole keyboard is.
func TestKeyMashingIsLow(t *testing.T) {
	for _, password := range []string{
		"dfssfdsdfsfdfsd", "jkljlkjkljlkjkl", "fgdsafdgsafdgas", "aksjdhfkajsdhf",
		"asdfjklasdfjkl", "zxcvzxcvzcxvzx", "sdfsdfsdfsdf", "ASDFghjklASDFgh",
		// On the Russian layout.
		"ЫВАпывапываы", "фывапролфывапрол", "ролджоролдждлор",
	} {
		if got := Estimate(password); got.Level > 1 {
			t.Errorf("%q: level %d (%.1f bits), want at most 1", password, got.Level, got.Bits)
		}
	}
	// Longer, a mash is still no more than fair, as what it repeats is
	// found out; what is in it that is not a mash is not lost.
	for _, password := range []string{"dfssfdsdfsfdfsdfdsfsdfdsfsdfsd", "aksjdhfkajsdhfkajsdhfkajsdhfk"} {
		if got := Estimate(password); got.Level > 2 {
			t.Errorf("%q: level %d (%.1f bits), want at most 2", password, got.Level, got.Bits)
		}
	}
	if got := Estimate("dfssfd7#kQ9mP2vL"); got.Level != Levels-1 {
		t.Errorf("a mash with a unique part: level %d (%.1f bits)", got.Level, got.Bits)
	}
	// Few different characters in no place of the keyboard: still few.
	if got := Estimate("xyxzzyxyzxyzzx"); got.Level > 2 {
		t.Errorf("few characters: level %d (%.1f bits), want at most 2", got.Level, got.Bits)
	}
	// Random characters all over the keyboard are not taken for a mash.
	for _, password := range []string{"vkqzmwpdhx", "hjkqweruiopl0", "pQzXmWvKd", "73940182657391840"} {
		if got := Estimate(password); got.Level < 3 {
			t.Errorf("%q: level %d (%.1f bits), want at least 3", password, got.Level, got.Bits)
		}
	}
}

// TestHighestLevelNeedsTheUsualKinds checks that a password without any of
// the kinds of characters the usual advice asks for, lower and upper case, a
// digit and a special character, is not given the highest level, however
// many bits it has, and is given it with all of them.
func TestHighestLevelNeedsTheUsualKinds(t *testing.T) {
	// The same password, each time with one kind less.
	whole := "Xq7#vKmZp2$wLtRb"
	if got := Estimate(whole); got.Level != Levels-1 {
		t.Fatalf("%q: level %d (%.1f bits), want the highest", whole, got.Level, got.Bits)
	}
	for name, password := range map[string]string{
		"no upper case":        "xq7#vkmzp2$wltrb",
		"no lower case":        "XQ7#VKMZP2$WLTRB",
		"no digit":             "Xq#vKmZp$wLtRbYn",
		"no special character": "Xq7vKmZp2wLtRbYn",
		"letters only":         "XqvKmZpwLtRbYnJd",
		"digits only":          "73940182657391840",
	} {
		got := Estimate(password)
		if got.Level != Levels-2 {
			t.Errorf("%s, %q: level %d (%.1f bits), want %d: it has the bits, but not the kinds", name, password, got.Level, got.Bits, Levels-2)
		}
		if got.Bits < thresholds[len(thresholds)-1] {
			t.Errorf("%s, %q: only %.1f bits, which is not what this test is of", name, password, got.Bits)
		}
	}
	// A password with few bits is not helped by having every kind.
	if got := Estimate("Pa$$w0rd"); got.Level != 0 {
		t.Errorf("Pa$$w0rd: level %d", got.Level)
	}
	// Letters of a script without case are of both cases, and so are what a
	// password may be made of to be given the highest level.
	if got := Estimate("密码很长很长很长的密码7#"); got.Level != Levels-1 {
		t.Errorf("a password in a script without case: level %d (%.1f bits), want the highest", got.Level, got.Bits)
	}
	// A space is a special character, and the case of Russian letters counts.
	if got := Estimate("Мой Длинный Пароль 7 Из Слов Vk"); got.Level != Levels-1 {
		t.Errorf("a phrase with a space: level %d (%.1f bits)", got.Level, got.Bits)
	}
}

func TestKindsOf(t *testing.T) {
	for password, want := range map[string]Kinds{
		"":        {},
		"abc":     {Lower: true},
		"ABC":     {Upper: true},
		"123":     {Digit: true},
		"#! ":     {Special: true},
		"aB1#":    {Lower: true, Upper: true, Digit: true, Special: true},
		"пароль":  {Lower: true},
		"Пароль7": {Lower: true, Upper: true, Digit: true},
		"日本語":     {Lower: true, Upper: true},
		"日本語 7":   {Lower: true, Upper: true, Digit: true, Special: true},
		"😀":       {Special: true},
		"٣":       {Digit: true},
	} {
		if got := KindsOf(password); got != want {
			t.Errorf("KindsOf(%q) = %+v, want %+v", password, got, want)
		}
	}
}
