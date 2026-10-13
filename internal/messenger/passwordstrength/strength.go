// SPDX-License-Identifier: Unlicense OR MIT

// Package passwordstrength estimates how hard a password is to guess, for an
// indicator beside the field it is typed in. It is a rough one: the password
// is cut into the parts it is most cheaply made of, each with the number of
// guesses it takes to find it, and those add up as bits.
//
//   - A common password, a common word or a first name, in English or in
//     Russian, even typed in the other keyboard layout (ghbdtn for привет),
//     in capitals or with digits for letters (p4ssw0rd), is a guess among
//     those that are as common as it is.
//   - A run of the same characters, a sequence (abcd, 4321), a walk over the
//     keyboard (qwerty, йцукен) and a year or a date are a few guesses each.
//   - Keys pressed in a small area of the keyboard, whichever they are and in
//     whatever order (dfssfdsdfsfd, aksjdhfkajsdhf), and a string made of
//     a few different characters, are not unique characters: each key of a
//     mash is a bit and a half, and what is past a dozen of them half that.
//   - A run of letters that reads as a word, which the short dictionaries
//     here do not have, takes about three bits a letter, not the five of
//     letters at random.
//   - Anything else is a character from the alphabets the password uses.
//
// Whatever is found of the first kind lowers the estimate and what is none
// of them, unique, raises it. The highest level is given only to a password
// with the usual kinds of characters, lower and upper case, a digit and a
// special character, however long it is. It never says that a password is
// not allowed: it is an indicator, not a rule.
package passwordstrength

import (
	"math"
	"strings"
	"sync"
	"unicode"
)

// Levels is the number of levels an estimate has, from 0 to Levels-1.
const Levels = 5

// The bits at which each level starts: a password of 4 random lowercase
// letters is the first, 6 the second and 8 the third, and 10 are enough
// bits for the last, which it takes the usual kinds of characters to be
// given (see kinds). Four words that are none of the dictionaries' are well
// past the bits, though they are not asked for.
var thresholds = [Levels - 1]float64{18, 28, 36, 44}

// Result is an estimate.
type Result struct {
	// Bits is the estimate of the guesses it takes, as their binary
	// logarithm.
	Bits float64
	// Level is how strong the password is, from 0, the weakest, to
	// Levels-1. An empty password is level 0 with no bits.
	Level int
}

// Kinds are the kinds of characters the usual advice asks a password to have
// all of, for it to be as strong as it can be said to be.
type Kinds struct {
	Lower, Upper, Digit, Special bool
}

// All reports whether the password has every kind.
func (k Kinds) All() bool { return k.Lower && k.Upper && k.Digit && k.Special }

// KindsOf returns the kinds of characters password has. A letter that has no
// case, as those of many scripts, differs in none, and counts for both the
// lower and the upper case; whatever is neither a letter nor a digit, a space
// included, is a special character.
func KindsOf(password string) Kinds {
	var k Kinds
	for _, r := range password {
		switch {
		case unicode.IsLower(r):
			k.Lower = true
		case unicode.IsUpper(r), unicode.IsTitle(r):
			k.Upper = true
		case unicode.IsLetter(r):
			k.Lower, k.Upper = true, true
		case unicode.IsDigit(r):
			k.Digit = true
		default:
			k.Special = true
		}
	}
	return k
}

// Estimate estimates password.
func Estimate(password string) Result {
	runes := []rune(password)
	if len(runes) == 0 {
		return Result{}
	}
	load()
	bits := estimate(runes)
	level := 0
	for level < len(thresholds) && bits >= thresholds[level] {
		level++
	}
	// However many bits it has, a password does not have the highest level
	// without the usual kinds of characters: it is what the advice is.
	if level == Levels-1 && !KindsOf(password).All() {
		level--
	}
	return Result{Bits: bits, Level: level}
}

// piece is a part of the password and what finding it takes.
type piece struct {
	end  int
	bits float64
}

// estimate finds the cheapest way to make the password of the parts that
// match, and the characters between them.
func estimate(runes []rune) float64 {
	n := len(runes)
	card := cardinality(runes)
	brute := math.Log2(card)
	const unreachable = math.MaxFloat64
	best := make([]float64, n+1)
	parts := make([]int, n+1)
	for i := 1; i <= n; i++ {
		best[i] = unreachable
	}
	for i := 0; i < n; i++ {
		if best[i] == unreachable {
			continue
		}
		relax := func(end int, bits float64, matched int) {
			// A pass of the parts, over the order they could come in, is
			// counted at the end.
			if total := best[i] + bits; total < best[end] {
				best[end], parts[end] = total, parts[i]+matched
			}
		}
		relax(i+1, brute, 0)
		for _, p := range matches(runes, i, card) {
			relax(p.end, p.bits, 1)
		}
	}
	bits := best[n]
	if k := parts[n]; k > 1 {
		// The parts could have come in any order.
		for f := 2; f <= k; f++ {
			bits += math.Log2(float64(f))
		}
	}
	return bits
}

// cardinality is the number of characters the alphabets of the password
// have: the ones its characters are of.
func cardinality(runes []rune) float64 {
	var lower, upper, digits, symbols, cyrLower, cyrUpper, other bool
	for _, r := range runes {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digits = true
		case r < 128:
			symbols = true
		case unicode.Is(unicode.Cyrillic, r) && unicode.IsLower(r):
			cyrLower = true
		case unicode.Is(unicode.Cyrillic, r):
			cyrUpper = true
		default:
			other = true
		}
	}
	size := 0.0
	for _, set := range []struct {
		on   bool
		size float64
	}{{lower, 26}, {upper, 26}, {digits, 10}, {symbols, 33}, {cyrLower, 33}, {cyrUpper, 33}, {other, 100}} {
		if set.on {
			size += set.size
		}
	}
	return max(size, 10)
}

// matches returns the parts of the password that start at i, each with the
// bits it takes to guess.
func matches(runes []rune, i int, card float64) []piece {
	var out []piece
	out = append(out, dictionary(runes, i)...)
	out = append(out, repeat(runes, i, math.Log2(card))...)
	out = append(out, sequence(runes, i)...)
	out = append(out, walk(runes, i)...)
	out = append(out, date(runes, i)...)
	out = append(out, wordlike(runes, i)...)
	out = append(out, mash(runes, i)...)
	out = append(out, fewSymbols(runes, i, card)...)
	return out
}

// caseBits is what the capitals of s add: nothing for none, a bit for the
// first letter or all of them, and the place of the ones among others
// otherwise.
func caseBits(s []rune) float64 {
	upper, letters := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	switch {
	case upper == 0:
		return 0
	case upper == letters, upper == 1 && unicode.IsUpper(s[0]):
		return 1
	}
	return 1 + math.Log2(float64(max(letters, 2)))
}

// dictionary matches the words of the dictionaries, as they are, with
// digits and signs for letters, and typed in the other layout.
func dictionary(runes []rune, i int) []piece {
	var out []piece
	for end := i + 3; end <= len(runes) && end-i <= maxWord; end++ {
		part := runes[i:end]
		lower := []rune(strings.ToLower(string(part)))
		extra := caseBits(part)
		if bits, ok := words[string(lower)]; ok {
			out = append(out, piece{end, bits + 1 + extra})
			continue
		}
		if swapped, n := unleet(lower); n > 0 {
			found := false
			for _, s := range swapped {
				if bits, ok := words[s]; ok {
					out = append(out, piece{end, bits + 1 + extra + float64(n)})
					found = true
					break
				}
			}
			if found {
				continue
			}
		}
		if s, ok := otherLayout(lower); ok {
			if bits, ok := words[s]; ok {
				out = append(out, piece{end, bits + 3 + extra})
			}
		}
	}
	return out
}

// unleet returns the ways to read the digits and signs of a word as the
// letters they look like, and how many it changed. 1 is an i or an l.
func unleet(s []rune) ([]string, int) {
	count := 0
	for _, r := range s {
		if _, ok := leet[r]; ok {
			count++
		}
	}
	if count == 0 {
		return nil, 0
	}
	var one, other strings.Builder
	for _, r := range s {
		switch {
		case r == '1':
			one.WriteRune('i')
			other.WriteRune('l')
		case leet[r] != 0:
			one.WriteRune(leet[r])
			other.WriteRune(leet[r])
		default:
			one.WriteRune(r)
			other.WriteRune(r)
		}
	}
	return []string{one.String(), other.String()}, count
}

// leet is the letters that digits and signs look like.
var leet = map[rune]rune{'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's', '7': 't', '8': 'b', '@': 'a', '$': 's', '!': 'i'}

// otherLayout is a word typed in the other of the English and the Russian
// keyboard layouts, as it was meant: whole, or not at all.
func otherLayout(s []rune) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		m, ok := layout[r]
		if !ok {
			return "", false
		}
		b.WriteRune(m)
	}
	return b.String(), true
}

// repeat matches a character, or two or three, said again: aaaa, abab.
func repeat(runes []rune, i int, brute float64) []piece {
	var out []piece
	for unit := 1; unit <= 3; unit++ {
		if i+2*unit > len(runes) {
			break
		}
		end := i + unit
		for end+unit <= len(runes) && equal(runes[i:i+unit], runes[end:end+unit]) {
			end += unit
		}
		times := (end - i) / unit
		if times >= 2 && end-i >= 3 {
			out = append(out, piece{end, float64(unit)*brute + math.Log2(float64(times)) + 1})
		}
	}
	return out
}

func equal(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sequence matches characters that follow one another, up or down: abcd,
// 7654.
func sequence(runes []rune, i int) []piece {
	if i+2 >= len(runes) {
		return nil
	}
	delta := runes[i+1] - runes[i]
	if delta != 1 && delta != -1 {
		return nil
	}
	if !alnum(runes[i]) {
		return nil
	}
	end := i + 1
	for end < len(runes) && runes[end]-runes[end-1] == delta && alnum(runes[end]) {
		end++
	}
	if end-i < 3 {
		return nil
	}
	start := 26.0
	if runes[i] >= '0' && runes[i] <= '9' {
		start = 10
	}
	var out []piece
	for e := i + 3; e <= end; e++ {
		out = append(out, piece{e, math.Log2(start) + 1 + math.Log2(float64(e-i))})
	}
	return out
}

func alnum(r rune) bool {
	return r >= '0' && r <= '9' || unicode.IsLetter(r)
}

// walk matches a walk over a row of the keyboard: qwerty, asdf, йцукен,
// forward or back.
func walk(runes []rune, i int) []piece {
	var out []piece
	for end := i + 4; end <= len(runes); end++ {
		s := strings.ToLower(string(runes[i:end]))
		if !onRow(s) {
			break
		}
		out = append(out, piece{end, math.Log2(50) + math.Log2(float64(end-i)) + 1 + caseBits(runes[i:end])})
	}
	return out
}

func onRow(s string) bool {
	rev := reverse(s)
	for _, row := range keyboardRows {
		if strings.Contains(row, s) || strings.Contains(row, rev) {
			return true
		}
	}
	return false
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// date matches a year, 1900 to 2039, and a date of six or eight digits.
func date(runes []rune, i int) []piece {
	digits := 0
	for i+digits < len(runes) && runes[i+digits] >= '0' && runes[i+digits] <= '9' {
		digits++
	}
	var out []piece
	num := func(a, b int) int {
		n := 0
		for _, r := range runes[i+a : i+b] {
			n = n*10 + int(r-'0')
		}
		return n
	}
	if digits >= 4 {
		if y := num(0, 4); y >= 1900 && y <= 2039 {
			out = append(out, piece{i + 4, math.Log2(140)})
		}
	}
	for _, size := range []int{6, 8} {
		if digits < size {
			continue
		}
		// ddmmyy, ddmmyyyy, and the year first.
		day, month := num(0, 2), num(2, 4)
		yearFirst := size == 8 && num(0, 4) >= 1900 && num(0, 4) <= 2039 && num(4, 6) >= 1 && num(4, 6) <= 12 && num(6, 8) >= 1 && num(6, 8) <= 31
		if day >= 1 && day <= 31 && month >= 1 && month <= 12 || yearFirst {
			out = append(out, piece{i + size, math.Log2(31*12*100) + 1})
		}
	}
	return out
}

// wordlike matches a run of letters that reads as a word of a language,
// which the dictionaries do not have: it has vowels among its consonants,
// and not many of either in a row. Such letters are about three bits each,
// not what a letter at random is.
func wordlike(runes []rune, i int) []piece {
	if i > 0 && unicode.IsLetter(runes[i-1]) {
		return nil
	}
	end := i
	for end < len(runes) && unicode.IsLetter(runes[end]) {
		end++
	}
	if end-i < 5 {
		return nil
	}
	vowels, run := 0, 0
	for _, r := range runes[i:end] {
		if strings.ContainsRune(vowelLetters, unicode.ToLower(r)) {
			vowels++
			run = 0
			continue
		}
		run++
		if run >= 5 {
			return nil
		}
	}
	ratio := float64(vowels) / float64(end-i)
	if ratio < .25 || ratio > .7 {
		return nil
	}
	return []piece{{end, 3*float64(end-i) + caseBits(runes[i:end])}}
}

const vowelLetters = "aeiouyаеёиоуыэюя"

var loadOnce sync.Once

// load makes the dictionaries, once: the bits a guess among them takes.
func load() {
	loadOnce.Do(func() {
		words = map[string]float64{}
		add := func(list string, base float64) {
			for rank, word := range strings.Fields(list) {
				word = strings.ToLower(word)
				bits := math.Log2(float64(rank+2)) + base
				if old, ok := words[word]; !ok || bits < old {
					words[word] = bits
					if len([]rune(word)) > maxWord {
						maxWord = len([]rune(word))
					}
				}
			}
		}
		add(topPasswords, 0)
		add(commonPasswords, 0)
		add(commonWords, 3)
		add(commonNames, 3)
		keys = map[rune]key{}
		for _, row := range keyRows {
			for col, r := range []rune(row.keys) {
				keys[r] = key{x: float64(col) + row.offset, row: row.row}
			}
		}
		layout = map[rune]rune{}
		latin, cyrillic := []rune(latinRow), []rune(cyrillicRow)
		for i := range latin {
			layout[latin[i]] = cyrillic[i]
			layout[cyrillic[i]] = latin[i]
		}
	})
}

var (
	words   map[string]float64
	maxWord = 3
	layout  map[rune]rune
	// keys are the places of the keys.
	keys map[rune]key
)

// key is where a key is: its row, and how far from the left edge of the
// board, in keys.
type key struct {
	x   float64
	row int
}

// A press of a key in a small area of the keyboard costs a bit and a half,
// not what a key at random does: whoever mashes the keys stays on a few of
// them, and the same few come again and again, which a guess that knows
// of it uses.
const (
	mashBitsPerKey = 1.5
	// mashMaxArea is the area, in keys, of the largest bounding box of the
	// keys of a mash, and mashMinLength the shortest mash.
	mashMaxArea   = 10
	mashMinLength = 5
)

// mash matches keys that lie in a small area of the keyboard: asdf, dfssfd,
// aksjdhf. Whichever of the keys they are, and in whatever order, what they
// take to guess is the area they are in.
func mash(runes []rune, i int) []piece {
	var out []piece
	minX, maxX := math.Inf(1), math.Inf(-1)
	minRow, maxRow := math.MaxInt, math.MinInt
	for end := i; end < len(runes); end++ {
		key, ok := keys[unicode.ToLower(runes[end])]
		if !ok {
			break
		}
		minX, maxX = math.Min(minX, key.x), math.Max(maxX, key.x)
		minRow, maxRow = min(minRow, key.row), max(maxRow, key.row)
		area := (int(math.Round(maxX-minX)) + 1) * (maxRow - minRow + 1)
		if area > mashMaxArea {
			break
		}
		if length := end + 1 - i; length >= mashMinLength {
			perKey := math.Min(math.Log2(float64(area)), mashBitsPerKey)
			out = append(out, piece{end + 1, effectiveLength(length)*perKey + 3 + caseBits(runes[i:end+1])})
		}
	}
	return out
}

// effectiveLength is how many characters of a string the guess of a mash or of
// a string of few characters has to find out: a long one repeats itself, and
// what is past the first twelve takes half as much each.
func effectiveLength(n int) float64 {
	return float64(min(n, 12)) + float64(max(n-12, 0))/2
}

// maxSymbols is the most different characters of a string made of few.
const maxSymbols = 6

// fewSymbols matches a string made of a few different characters, in any
// order: which characters they are, among those the password's alphabets
// have, and which of them each place has.
func fewSymbols(runes []rune, i int, card float64) []piece {
	var out []piece
	seen := map[rune]bool{}
	for end := i; end < len(runes); end++ {
		seen[runes[end]] = true
		d := len(seen)
		if d > maxSymbols {
			break
		}
		length := end + 1 - i
		if d < 2 || length < 6 || float64(d) >= card {
			continue
		}
		bits := log2Choose(card, float64(d)) + effectiveLength(length)*math.Log2(float64(d))
		out = append(out, piece{end + 1, bits})
	}
	return out
}

// log2Choose is the binary logarithm of the ways to choose k of n.
func log2Choose(n, k float64) float64 {
	a, _ := math.Lgamma(n + 1)
	b, _ := math.Lgamma(k + 1)
	c, _ := math.Lgamma(n - k + 1)
	return (a - b - c) / math.Ln2
}
