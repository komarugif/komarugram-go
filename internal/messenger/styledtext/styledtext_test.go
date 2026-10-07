package styledtext

import (
	"image"
	"reflect"
	"strings"
	"testing"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
)

func layoutFragments(t *testing.T, shaper *text.Shaper, buffer *[]Cluster, width int, spans ...SpanStyle) ([]Fragment, image.Point) {
	t.Helper()
	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Constraints{Max: image.Pt(width, 10000)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	var fragments []Fragment
	style := Text(shaper, spans...)
	style.Clusters = buffer
	style.Decorate = func(_ layout.Context, f Fragment, draw func()) {
		// Copy: with a buffer, the next layout overwrites the clusters.
		f.Clusters = append([]Cluster(nil), f.Clusters...)
		fragments = append(fragments, f)
		draw()
	}
	return fragments, style.Layout(gtx, nil).Size
}

// Wrapped spans are laid out line by line. Every rune must belong to exactly
// one cluster, in order, and a reused cluster buffer must not change results.
func TestWrappedSpansCoverEveryRuneOnce(t *testing.T) {
	shaper := text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	long := strings.Repeat("word wrap текст ", 40)
	spans := []SpanStyle{
		{Size: 14, Content: long},
		{Size: 14, Font: font.Font{Weight: font.Bold}, Content: "bold middle " + long},
		{Size: 14, Content: "line\nbreak " + long},
	}
	var all []rune
	for _, s := range spans {
		all = append(all, []rune(s.Content)...)
	}
	total := len(all)
	var buffer []Cluster
	for _, width := range []int{120, 333, 800} {
		want, wantSize := layoutFragments(t, shaper, nil, width, spans...)
		next := 0
		lines := map[int]bool{}
		for _, f := range want {
			lines[f.Bounds.Min.Y] = true
			for _, c := range f.Clusters {
				if c.Start == next+1 && all[next] == '\n' {
					// Hard line breaks are not clusters of their own.
					next++
				}
				if c.Start != next || c.End <= c.Start {
					t.Fatalf("width %d: cluster %d..%d, want start %d", width, c.Start, c.End, next)
				}
				if !c.Bounds.In(f.Bounds.Inset(-2)) {
					t.Fatalf("width %d: cluster %v outside fragment %v", width, c.Bounds, f.Bounds)
				}
				next = c.End
			}
		}
		if next != total {
			t.Fatalf("width %d: clusters end at %d of %d runes", width, next, total)
		}
		if width == 120 && len(lines) < 20 {
			t.Fatalf("expected many wrapped lines, got %d", len(lines))
		}
		for range 2 {
			got, gotSize := layoutFragments(t, shaper, &buffer, width, spans...)
			if gotSize != wantSize || !reflect.DeepEqual(got, want) {
				t.Fatalf("width %d: reused cluster buffer changed the layout", width)
			}
		}
	}
}

// A line is shaped from the start of its span only; it must wrap as if the
// whole span were shaped, whatever glyphs and breaks the text has.
func TestLinePrefixWrapsAsWholeSpan(t *testing.T) {
	shaper := text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	texts := []string{
		strings.Repeat("Длинный пост канала с обычным текстом, который переносится. ", 30),
		strings.Repeat("iiii llll ", 200),
		strings.Repeat("W", 900) + " tail",
		strings.Repeat("short\n", 50) + strings.Repeat("x ", 400),
		strings.Repeat("ааааааааааааааааааааааааааааааааааааааа ", 40),
		// Combining marks take no width: a line holds more runes than
		// the estimate, and is shaped again from the whole span.
		strings.Repeat("e\u0301\u0301\u0301\u0301 ", 300),
	}
	for _, content := range texts {
		for _, width := range []int{37, 120, 333, 800} {
			spans := []SpanStyle{{Size: 14, Content: "lead "}, {Size: 14, Content: content}, {Size: 14, Font: font.Font{Weight: font.Bold}, Content: " end"}}
			got, gotSize := layoutFragments(t, shaper, nil, width, spans...)
			shapeWholeSpans = true
			want, wantSize := layoutFragments(t, shaper, nil, width, spans...)
			shapeWholeSpans = false
			if gotSize != wantSize || !reflect.DeepEqual(got, want) {
				t.Fatalf("%.20q at %d: %d fragments %v, want %d %v", content, width, len(got), gotSize, len(want), wantSize)
			}
		}
	}
}

// Collapsed quotes must stop both drawing and hit testing at three visual
// lines, including when a line contains several styles or wraps a long span.
func TestMaxLinesStopsBeforeHiddenContent(t *testing.T) {
	shaper := text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	for _, content := range []string{"one\ntwo\nthree\nHIDDEN", "one\n\ntwo\nHIDDEN", strings.Repeat("wrapped word ", 1000)} {
		gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Constraints{Max: image.Pt(120, 10000)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
		style := Text(shaper, SpanStyle{Content: "bold ", Size: 14, Font: font.Font{Weight: font.Bold}}, SpanStyle{Content: content, Size: 14})
		style.MaxLines = 3
		lines := map[int]bool{}
		end := 0
		style.Decorate = func(_ layout.Context, f Fragment, draw func()) {
			lines[f.Bounds.Min.Y] = true
			for _, c := range f.Clusters {
				end = max(end, c.End)
			}
			draw()
		}
		style.Layout(gtx, nil)
		if len(lines) != 3 || end >= len([]rune("bold "+content)) {
			t.Fatalf("hidden content exposed: %d lines, %d runes", len(lines), end)
		}
	}
}

func TestLongCodeTokenWrapsOneLinePerFragment(t *testing.T) {
	shaper := text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	code := "func greet() {\n    fmt.Println(\"Привет, мир! 👋\")\n}"
	for width := 80; width <= 240; width += 10 {
		fragments, _ := layoutFragments(t, shaper, nil, width, SpanStyle{Content: code, Size: 20, Font: font.Font{Typeface: "Go Mono"}})
		for _, f := range fragments {
			if f.Bounds.Dy() > 35 {
				t.Fatalf("width %d: fragment spans multiple lines: %v", width, f.Bounds)
			}
		}
	}
}

// A shifted span is set lower on its line, as a subscript, and its line is
// as high as it reaches.
func TestShiftMovesASpanDown(t *testing.T) {
	shaper := text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	plain, size := layoutFragments(t, shaper, nil, 800, SpanStyle{Size: 16, Content: "H"}, SpanStyle{Size: 12, Content: "2"}, SpanStyle{Size: 16, Content: "O"})
	shifted, shiftedSize := layoutFragments(t, shaper, nil, 800, SpanStyle{Size: 16, Content: "H"}, SpanStyle{Size: 12, Content: "2", Shift: 7}, SpanStyle{Size: 16, Content: "O"})
	if len(plain) != 3 || len(shifted) != 3 {
		t.Fatalf("%d and %d fragments", len(plain), len(shifted))
	}
	if d := shifted[1].Bounds.Min.Y - plain[1].Bounds.Min.Y; d != 7 {
		t.Fatalf("the subscript moved %d down", d)
	}
	if shifted[1].Clusters[0].Bounds.Min.Y != shifted[1].Bounds.Min.Y {
		t.Fatal("the subscript's clusters stayed")
	}
	if shifted[0].Bounds != plain[0].Bounds || shifted[2].Bounds != plain[2].Bounds {
		t.Fatal("the spans around it moved")
	}
	if shiftedSize.Y < plain[1].Bounds.Dy()+7 || shiftedSize.Y < size.Y {
		t.Fatalf("the line is %d high, the subscript reaches %d", shiftedSize.Y, plain[1].Bounds.Dy()+7)
	}
}

// A box is an object of its size in its line, as an inline formula: one
// cluster of all its text, its baseline on the text's, and to the next line
// when it does not fit.
func TestBoxSitsOnTheBaseline(t *testing.T) {
	shaper := text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	box := &Box{Size: image.Pt(60, 40), Ascent: 30}
	frags, size := layoutFragments(t, shaper, nil, 800, SpanStyle{Size: 16, Content: "ab "}, SpanStyle{Size: 16, Content: `\frac{1}{2}`, Box: box}, SpanStyle{Size: 16, Content: " cd"})
	if len(frags) != 3 {
		t.Fatalf("%d fragments", len(frags))
	}
	b := frags[1]
	if b.Bounds.Dx() != 60 || b.Bounds.Dy() != 40 || len(b.Clusters) != 1 || b.Clusters[0].End-b.Clusters[0].Start != len([]rune(`\frac{1}{2}`)) {
		t.Fatalf("the box is %v, clusters %+v", b.Bounds, b.Clusters)
	}
	plain, _ := layoutFragments(t, shaper, nil, 800, SpanStyle{Size: 16, Content: "ab "})
	textAscent := plain[0].Bounds.Dy() * 3 / 4
	// The text's baseline is 30 under the box's top.
	if top := frags[0].Bounds.Min.Y; top < b.Bounds.Min.Y+30-textAscent-4 || top > b.Bounds.Min.Y+30-textAscent+4 {
		t.Fatalf("the text's top is at %d, the box's at %d", top, b.Bounds.Min.Y)
	}
	if size.Y < 40 {
		t.Fatalf("the line is %d high", size.Y)
	}
	wrapped, _ := layoutFragments(t, shaper, nil, 70, SpanStyle{Size: 16, Content: "ab "}, SpanStyle{Size: 16, Content: "x", Box: box})
	if wrapped[1].Bounds.Min.X != 0 || wrapped[1].Bounds.Min.Y <= wrapped[0].Bounds.Min.Y {
		t.Fatalf("a box that does not fit stays at %v", wrapped[1].Bounds)
	}
}
