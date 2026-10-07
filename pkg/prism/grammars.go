// SPDX-License-Identifier: Unlicense OR MIT

// Package prism tokenizes code as Prism.js does, for highlighting. It is a
// port of libprisma's tokenizer, the one Telegram Desktop highlights code
// with, to Go over regexp2, with Prism.js 1.29.0's grammars
// (grammars.dat.gz, which generate/ makes). libprisma and Prism.js are MIT;
// see LICENSE.prism.
package prism

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/dlclark/regexp2"
)

//go:embed grammars.dat.gz
var embedded []byte

// Grammars are the patterns, grammars and languages of a grammars.dat. They
// are safe for concurrent use; a pattern compiles when it is first used.
type Grammars struct {
	patterns  []pattern
	grammars  []grammar
	languages map[string]language
	// matchTimeout bounds each match of a pattern.
	matchTimeout time.Duration
}

type language struct {
	title   string
	grammar int
}

type grammar struct {
	tokens []grammarToken
}

type grammarToken struct {
	name     string
	patterns []int
}

type pattern struct {
	source             string
	options            regexp2.RegexOptions
	lookbehind, greedy bool
	alias              string
	// inside is the grammar that tokenizes what the pattern matches, or -1.
	inside int

	once sync.Once
	re   *regexp2.Regexp
	err  error
}

// Embedded loads the grammars built into the package. matchTimeout bounds
// each match of a pattern; zero means no bound.
func Embedded(matchTimeout time.Duration) (*Grammars, error) {
	r, err := gzip.NewReader(bytes.NewReader(embedded))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Load(data, matchTimeout)
}

var errCorrupt = errors.New("prism: grammars corrupt")

// reader reads grammars.dat: little-endian counts, and strings that begin
// with their length, one byte below 254 or else 254 and three bytes.
type reader struct {
	data []byte
	err  error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n > len(r.data) {
		r.err = errCorrupt
		return nil
	}
	b := r.data[:n]
	r.data = r.data[n:]
	return b
}

func (r *reader) uint8() int {
	if b := r.take(1); b != nil {
		return int(b[0])
	}
	return 0
}

func (r *reader) uint16() int {
	if b := r.take(2); b != nil {
		return int(binary.LittleEndian.Uint16(b))
	}
	return 0
}

func (r *reader) string() string {
	n := r.uint8()
	if n >= 254 {
		n = r.uint8() | r.uint8()<<8 | r.uint8()<<16
	}
	return string(r.take(n))
}

// Load reads a grammars.dat, as libprisma's LanguageTree does, and checks
// that everything in it points at what is there.
func Load(data []byte, matchTimeout time.Duration) (*Grammars, error) {
	r := &reader{data: data}
	g := &Grammars{languages: map[string]language{}, matchTimeout: matchTimeout}
	g.patterns = make([]pattern, r.uint16())
	for i := range g.patterns {
		if err := parsePattern(&g.patterns[i], r.string()); err != nil && r.err == nil {
			r.err = fmt.Errorf("prism: pattern %d: %w", i, err)
		}
	}
	g.grammars = make([]grammar, r.uint16())
	for i := range g.grammars {
		tokens := make([]grammarToken, r.uint8())
		for j := range tokens {
			tokens[j].name = r.string()
			tokens[j].patterns = make([]int, r.uint8())
			for k := range tokens[j].patterns {
				id := r.uint16()
				if id >= len(g.patterns) {
					r.err = errCorrupt
				}
				tokens[j].patterns[k] = id
			}
		}
		g.grammars[i].tokens = tokens
	}
	for n := r.uint16(); n > 0; n-- {
		name, title, id := r.string(), r.string(), r.uint16()
		if id >= len(g.grammars) {
			r.err = errCorrupt
		}
		g.languages[name] = language{title: title, grammar: id}
	}
	for i := range g.patterns {
		if g.patterns[i].inside >= len(g.grammars) {
			r.err = errCorrupt
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	return g, nil
}

// parsePattern reads "/source/flags,alias,inside", where flags are the
// regular expression's, then l for a lookbehind group and y for greedy.
func parsePattern(p *pattern, item string) error {
	begin, end := strings.IndexByte(item, '/'), strings.LastIndexByte(item, '/')
	if begin < 0 || end <= begin {
		return errCorrupt
	}
	options := item[end+1:]
	first, last := strings.IndexByte(options, ','), strings.LastIndexByte(options, ',')
	if first < 0 || last == first {
		return errCorrupt
	}
	p.source = joinSurrogates(item[begin+1 : end])
	p.alias = options[first+1 : last]
	p.inside = -1
	if inside := options[last+1:]; inside != "" {
		n, err := strconv.Atoi(inside)
		if err != nil || n < 0 {
			return errCorrupt
		}
		p.inside = n
	}
	p.options = regexp2.ECMAScript
	for _, c := range options[:first] {
		switch c {
		case 'l':
			p.lookbehind = true
		case 'y':
			p.greedy = true
		case 'i':
			p.options |= regexp2.IgnoreCase
		case 'm':
			p.options |= regexp2.Multiline
		}
	}
	return nil
}

// joinSurrogates writes a character beyond the BMP, which the generator
// writes as the \u escapes of its surrogate pair, as itself: regexp2
// matches runes, and in ECMAScript mode has no other escape for it.
func joinSurrogates(source string) string {
	if !strings.Contains(source, `\u`) {
		return source
	}
	var out strings.Builder
	for i := 0; i < len(source); i++ {
		if source[i] == '\\' && i+1 < len(source) {
			if hi, ok := hexEscape(source, i); ok && utf16.IsSurrogate(hi) {
				if lo, ok := hexEscape(source, i+6); ok {
					if r := utf16.DecodeRune(hi, lo); r != '�' {
						out.WriteRune(r)
						i += 11
						continue
					}
				}
			}
			// An escaped backslash is not the start of an escape.
			out.WriteString(source[i : i+2])
			i++
			continue
		}
		out.WriteByte(source[i])
	}
	return out.String()
}

// hexEscape is the code unit of the \uXXXX escape at i.
func hexEscape(s string, i int) (rune, bool) {
	if i+6 > len(s) || s[i] != '\\' || s[i+1] != 'u' {
		return 0, false
	}
	n, err := strconv.ParseUint(s[i+2:i+6], 16, 16)
	return rune(n), err == nil
}

// regexp is the pattern compiled, the first time it is asked for.
func (g *Grammars) regexp(p *pattern) (*regexp2.Regexp, error) {
	p.once.Do(func() {
		p.re, p.err = regexp2.Compile(p.source, p.options)
		if p.re != nil && g.matchTimeout > 0 {
			p.re.MatchTimeout = g.matchTimeout
		}
	})
	return p.re, p.err
}

// Languages are the names of the languages the grammars know, with their
// titles; aliases are among them.
func (g *Grammars) Languages() map[string]string {
	out := make(map[string]string, len(g.languages))
	for name, l := range g.languages {
		if l.title != "" {
			out[name] = l.title
		}
	}
	return out
}

// Has reports whether the grammars know language.
func (g *Grammars) Has(language string) bool {
	_, ok := g.languages[language]
	return ok
}
