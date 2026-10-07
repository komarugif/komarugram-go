// SPDX-License-Identifier: Unlicense OR MIT

package prism

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func embeddedGrammars(t testing.TB) *Grammars {
	t.Helper()
	g, err := Embedded(0)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// flat is spans as testdata/prismjs.json has them: type, alias and length.
func flat(spans []Span) [][3]any {
	var out [][3]any
	for _, s := range spans {
		out = append(out, [3]any{s.Type, s.Alias, float64(s.End - s.Start)})
	}
	return out
}

// The tokens are Prism.js's own (testdata/prismjs.json, which
// generate/expected.js writes), Cyrillic identifiers, 1C:Enterprise and
// characters beyond the BMP included.
func TestTokenizeAsPrismJS(t *testing.T) {
	g := embeddedGrammars(t)
	var samples [][2]string
	var expected [][][3]any
	for name, v := range map[string]any{"testdata/samples.json": &samples, "testdata/prismjs.json": &expected} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	if len(samples) != len(expected) {
		t.Fatalf("%d samples, %d expected", len(samples), len(expected))
	}
	for i, s := range samples {
		spans, err := g.Tokenize(s[1], s[0], time.Time{})
		if err != nil {
			t.Errorf("%s: %v", s[0], err)
			continue
		}
		want, _ := json.Marshal(expected[i])
		have, _ := json.Marshal(flat(spans))
		if string(want) != string(have) {
			t.Errorf("%s:\n got  %s\n want %s", s[0], have, want)
		}
	}
}

// Spans cover the text in order, byte for byte, invalid UTF-8 too.
func TestSpansCoverText(t *testing.T) {
	g := embeddedGrammars(t)
	for _, text := range []string{"", "x", "let a = '\xff\xfe' + \"ж\xc3\" // \x80", strings.Repeat("😀 = 1;\n", 50)} {
		spans, err := g.Tokenize(text, "javascript", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		end := 0
		for _, s := range spans {
			if s.Start != end || s.End <= s.Start {
				t.Fatalf("%q: span %+v after %d", text, s, end)
			}
			end = s.End
		}
		if end != len(text) {
			t.Fatalf("%q: spans end at %d of %d", text, end, len(text))
		}
	}
	if _, err := g.Tokenize("x", "no-such-language", time.Time{}); !errors.Is(err, ErrUnknownLanguage) {
		t.Fatalf("unknown language: %v", err)
	}
}

// Text that makes Prism's grammars backtrack stops at the deadline, with
// what was found so far: TypeScript's grammar takes seconds over 4 KB of
// one letter.
func TestTokenizeStopsAtDeadline(t *testing.T) {
	// Each bound alone stops it: the deadline between matches, the match
	// timeout inside one. The deadline's text is shorter, so that no single
	// match takes long, slow as the race detector makes them: 1,000 letters
	// take 160 ms, and 4,000 take 3 s, in matches of up to a second.
	for _, c := range []struct {
		name     string
		text     string
		timeout  time.Duration
		deadline time.Duration
	}{
		{"deadline", strings.Repeat("a", 1000), 0, 20 * time.Millisecond},
		{"match timeout", strings.Repeat("a", 4000), 50 * time.Millisecond, time.Hour},
	} {
		text := c.text
		g, err := Embedded(c.timeout)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		spans, err := g.Tokenize(text, "typescript", start.Add(c.deadline))
		took := time.Since(start)
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("%s: not stopped, after %v: %v", c.name, took, err)
		}
		// regexp2 checks its timeout on a coarse clock.
		if took > 2*time.Second {
			t.Fatalf("%s: stopped after %v", c.name, took)
		}
		if n := len(spans); n == 0 || spans[n-1].End != len(text) {
			t.Fatalf("%s: spans do not cover the text: %+v", c.name, spans)
		}
	}
}

// Grammars are used from many goroutines at once, as windows do.
func TestTokenizeConcurrently(t *testing.T) {
	g := embeddedGrammars(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			lang := []string{"go", "javascript", "python", "markup"}[i%4]
			for range 20 {
				if _, err := g.Tokenize("x := \"a\" + <b>1</b> # c", lang, time.Time{}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}

func TestJoinSurrogates(t *testing.T) {
	for in, want := range map[string]string{
		`[\ud835\udd68\ud835\udd69]`: "[𝕨𝕩]",
		`\\ud835\udd68`:              `\\ud835\udd68`,
		`\u0041\ud835`:               `\u0041\ud835`,
		`a\u00e9b`:                   `a\u00e9b`,
	} {
		if got := joinSurrogates(in); got != want {
			t.Errorf("%s: %s, not %s", in, got, want)
		}
	}
}

func rawGrammars(t testing.TB) []byte {
	r, err := gzip.NewReader(bytes.NewReader(embedded))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A grammars.dat cut short or with an index out of range is an error, not a
// panic.
func TestLoadRejectsCorrupt(t *testing.T) {
	data := rawGrammars(t)
	for _, n := range []int{0, 1, 2, 100, len(data) / 2, len(data) - 1} {
		if _, err := Load(data[:n], 0); err == nil {
			t.Errorf("%d bytes loaded", n)
		}
	}
	// The first pattern's inside, a grammar beyond the last one.
	bad := append([]byte(nil), data...)
	bad[0], bad[1] = 1, 0
	item := []byte("/x/,,65535")
	bad = append(append(bad[:2], byte(len(item))), item...)
	bad = append(bad, 0, 0, 0, 0)
	if _, err := Load(bad, 0); err == nil {
		t.Error("a pattern inside a missing grammar loaded")
	}
}

func FuzzLoad(f *testing.F) {
	data := rawGrammars(f)
	f.Add(data[:4096], "javascript")
	f.Add([]byte("\x01\x00\x0a/(a)b/l,,0\x01\x00\x01\x01x\x01\x00\x00\x01\x00\x02js\x02JS\x00\x00"), "js")
	f.Fuzz(func(t *testing.T, data []byte, language string) {
		g, err := Load(data, 10*time.Millisecond)
		if err != nil {
			return
		}
		text := "let a = (b) => c; // d"
		spans, _ := g.Tokenize(text, language, time.Now().Add(100*time.Millisecond))
		if len(spans) > 0 && spans[len(spans)-1].End != len(text) {
			t.Fatalf("spans %+v", spans)
		}
	})
}
