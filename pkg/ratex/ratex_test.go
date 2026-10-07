// SPDX-License-Identifier: Unlicense OR MIT

package ratex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestRuntime(t *testing.T, limits Limits) *Runtime {
	t.Helper()
	r, err := NewRuntime(context.Background(), limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(context.Background()) })
	return r
}

// A formula is laid out into glyphs of KaTeX's fonts and lines, in em,
// without a colour of its own; one that sets its colour keeps it.
func TestLayout(t *testing.T) {
	r := newTestRuntime(t, DefaultLimits)
	list, err := r.Layout(context.Background(), `\frac{a}{b} = \sqrt{x^2 + y^2}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if list.Width <= 0 || list.Height <= 0 || list.Depth <= 0 {
		t.Fatalf("box %v × %v + %v", list.Width, list.Height, list.Depth)
	}
	kinds := map[string]int{}
	fonts := map[string]bool{}
	for _, it := range list.Items {
		kinds[it.Type]++
		if it.Type == GlyphPath {
			fonts[it.Font] = true
			if it.Color.Own() {
				t.Fatalf("a glyph has a colour of its own: %+v", it.Color)
			}
		}
	}
	if kinds[GlyphPath] < 6 || kinds[Line]+kinds[Path] == 0 || !fonts["Math-Italic"] || !fonts["Main-Regular"] {
		t.Fatalf("items %v in fonts %v", kinds, fonts)
	}
	list, err = r.Layout(context.Background(), `\color{red}{x}`, false)
	if err != nil || len(list.Items) == 0 || !list.Items[0].Color.Own() || list.Items[0].Color.R != 1 {
		t.Fatalf("a red x: %+v, %v", list, err)
	}
}

// A formula RaTeX cannot read is an error with its message; one too long
// is not laid out.
func TestLayoutErrors(t *testing.T) {
	r := newTestRuntime(t, DefaultLimits)
	_, err := r.Layout(context.Background(), `\frac{1}{`, false)
	var e *Error
	if !errors.As(err, &e) || e.Message == "" {
		t.Fatalf("an unclosed fraction: %v", err)
	}
	if _, err := r.Layout(context.Background(), strings.Repeat("x", DefaultLimits.MaxSource+1), false); !errors.Is(err, ErrTooLong) {
		t.Fatalf("a long formula: %v", err)
	}
	// The runtime goes on after errors.
	if _, err := r.Layout(context.Background(), `x^2`, false); err != nil {
		t.Fatal(err)
	}
}

// Hostile formulas stay bounded: deep nesting and endless macros are
// errors, and none takes long or the memory past the limit.
func TestLayoutHostile(t *testing.T) {
	r := newTestRuntime(t, DefaultLimits)
	hostile := []string{
		strings.Repeat(`\frac{1}{`, 200) + "x" + strings.Repeat("}", 200),
		strings.Repeat(`\sqrt{`, 1000) + "x" + strings.Repeat("}", 1000),
		strings.Repeat("x^{", 3000) + "x" + strings.Repeat("}", 3000),
		`\def\a{\a}\a`,
		`\def\a{\a\a}\a`,
		`\newcommand{\b}{\b\b}\b`,
		`\rule{100000em}{100000em}`,
		strings.Repeat("x+", 4000),
	}
	for _, f := range hostile {
		start := time.Now()
		list, err := r.Layout(context.Background(), f, true)
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%.30q took %v", f, d)
		}
		if err == nil && list == nil {
			t.Errorf("%.30q: no list and no error", f)
		}
	}
}

// KaTeX's fonts have outlines for what formulas draw, at ASCII's places for
// the mathematical letters, and none for Cyrillic, which the host draws.
func TestGlyphs(t *testing.T) {
	if o := glyph("Main-Regular", 'x'); o == nil || len(o.segments) == 0 || o.upem == 0 {
		t.Fatalf("Main-Regular x: %+v", o)
	}
	if glyph("Main-Regular", 'Ж') != nil {
		t.Fatal("KaTeX's Main-Regular has a Ж")
	}
	if glyph("No-Such", 'x') != nil {
		t.Fatal("a font KaTeX has not")
	}
	for _, c := range []struct {
		font string
		code rune
		want rune
	}{{"Main-Bold", 0x1D400, 'A'}, {"Math-Italic", 0x1D44E, 'a'}, {"AMS-Regular", 0x1D538, 'A'}, {"Main-Bold", 0x1D7CF, '1'}, {"Main-Regular", 0x1D400, 0x1D400}} {
		if got := ttfChar(c.font, c.code); got != c.want {
			t.Errorf("%s %U: %q, not %q", c.font, c.code, got, c.want)
		}
	}
	if glyph("AMS-Regular", 0x1D538) == nil {
		t.Fatal("no outline for 𝔸 in AMS-Regular")
	}
}

// realFormulas are those of @richtextdemobot's articles, as messages carry
// them.
var realFormulas = []string{
	`E = mc^2`, `a^2 + b^2 = c^2`, `\int_0^\infty e^{-x^2} dx = \frac{\sqrt{\pi}}{2}`,
	`\sum_{i=1}^n i = \frac{n(n+1)}{2}`, `\frac{a}{b} = \sqrt{x^2 + y^2}`,
	`\begin{pmatrix} a & b \\ c & d \end{pmatrix}`, `f(x) = \begin{cases} 1 & x > 0 \\ 0 & x \le 0 \end{cases}`,
}

// BenchmarkLayout lays out the real formulas, after the module is
// compiled and instantiated; TestCompile tells how long that takes.
func BenchmarkLayout(b *testing.B) {
	r, err := NewRuntime(context.Background(), DefaultLimits)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close(context.Background())
	if _, err := r.Layout(context.Background(), "x", false); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		if _, err := r.Layout(context.Background(), realFormulas[i%len(realFormulas)], true); err != nil {
			b.Fatal(err)
		}
	}
}

// TestCompile tells how long compiling the module takes, without a cache.
func TestCompile(t *testing.T) {
	start := time.Now()
	r := newTestRuntime(t, DefaultLimits)
	compiled := time.Since(start)
	if _, err := r.Layout(context.Background(), realFormulas[2], true); err != nil {
		t.Fatal(err)
	}
	t.Logf("compiled in %v, the first formula in %v more; %d bytes gzipped", compiled, time.Since(start)-compiled, len(moduleGz))
}
