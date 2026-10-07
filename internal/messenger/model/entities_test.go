// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTextRunsBlockMetadataAndAdjacentBlocks(t *testing.T) {
	text := "👋codequoteend"
	runs := TextRuns(text, []Entity{
		{Kind: "pre", Offset: 2, Length: 4, Language: "go"},
		{Kind: "quote", Offset: 6, Length: 5, Collapsed: true},
		{Kind: "pre", Offset: 11, Length: 3, Language: "go"},
		{Kind: "bold", Offset: 7, Length: 2},
	})
	if len(runs) != 6 || runs[1].Block != 1 || !runs[1].Pre || !runs[1].Code || runs[1].Language != "go" ||
		!runs[2].Quote || !runs[2].Collapsed || !runs[3].Bold || runs[3].Block != runs[2].Block || runs[5].Block != 3 {
		t.Fatalf("block styles lost: %+v", runs)
	}
	adjacent := TextRuns("abcd", []Entity{{Kind: "pre", Length: 2}, {Kind: "pre", Offset: 2, Length: 2}})
	if len(adjacent) != 2 || adjacent[0].Block == adjacent[1].Block {
		t.Fatalf("adjacent blocks merged: %+v", adjacent)
	}
}

func TestTextRunsHostileRangesAndRedundantStyles(t *testing.T) {
	text := "A👋Б"
	entities := []Entity{
		{Kind: "pre", Offset: -1, Length: 2}, {Kind: "quote", Offset: 2, Length: 1},
		{Kind: "pre", Offset: 1, Length: 1}, {Kind: "pre", Offset: 1, Length: math.MaxInt},
		{Kind: "quote", Offset: math.MaxInt, Length: 2}, {Kind: "spoiler", Length: -1},
		{Kind: "bold", Offset: 1, Length: 0},
	}
	runs := TextRuns(text, entities)
	if len(runs) != 1 || runs[0] != (TextRun{Text: text}) {
		t.Fatalf("invalid ranges accepted: %+v", runs)
	}
	const n = 4096
	text = strings.Repeat("x", n*2)
	entities = make([]Entity, n)
	for i := range entities {
		entities[i] = Entity{Kind: "bold", Offset: i, Length: n*2 - i*2}
	}
	runs = TextRuns(text, entities)
	if len(runs) != 1 || runs[0].Text != text || !runs[0].Bold {
		t.Fatalf("redundant intervals produced %d shaping runs", len(runs))
	}
}

func TestTextRunsCrossingBlocksKeepStyles(t *testing.T) {
	runs := TextRuns("abcdef", []Entity{
		{Kind: "quote", Length: 4, Collapsed: true}, {Kind: "pre", Offset: 2, Length: 4, Language: "text"},
		{Kind: "spoiler", Offset: 1, Length: 4}, {Kind: "url", Length: 6, URL: "https://example.com"},
	})
	for _, r := range runs {
		if r.Block == 0 || r.URL != "https://example.com" {
			t.Fatalf("lost block/link: %+v", r)
		}
		if strings.ContainsAny(r.Text, "bcde") && !r.Spoiler {
			t.Fatal("overlapping block exposed spoiler")
		}
	}
}

func FuzzTextRunsHostile(f *testing.F) {
	f.Add("A👋Бcode", []byte{0, 0, 4, 0, 1, 0, 2, 0})
	f.Add("\xff\n👋", []byte{255, 255, 255, 127, 0, 0, 255, 255})
	f.Fuzz(func(t *testing.T, text string, data []byte) {
		if len(text) > 8192 || len(data) > 4096 {
			t.Skip()
		}
		kinds := []string{"bold", "italic", "spoiler", "pre", "quote", "url", "emoji"}
		var es []Entity
		for i := 0; i+4 <= len(data); i += 4 {
			es = append(es, Entity{Kind: kinds[i/4%len(kinds)], Offset: int(int16(binary.LittleEndian.Uint16(data[i:]))), Length: int(int16(binary.LittleEndian.Uint16(data[i+2:]))), Language: "go", Collapsed: true})
		}
		runs := TextRuns(text, es)
		var got strings.Builder
		units := 0
		for _, r := range runs {
			if r.Text == "" {
				t.Fatal("empty run")
			}
			if utf8.ValidString(text) && !utf8.ValidString(r.Text) {
				t.Fatal("split UTF-8")
			}
			start := units
			for _, ch := range r.Text {
				units++
				if ch > 0xffff {
					units++
				}
			}
			if r.Block != 0 {
				if r.Block < 1 || r.Block > len(es) {
					t.Fatal("invalid block ID")
				}
				e := es[r.Block-1]
				if e.Offset > start || e.Length <= 0 || e.Offset+e.Length < units {
					t.Fatal("block escaped its source interval")
				}
			}
			got.WriteString(r.Text)
		}
		if got.String() != text {
			t.Fatal("text changed")
		}
	})
}

func BenchmarkTextRunsOverlapping(b *testing.B) {
	const n = 4096
	text := strings.Repeat("x", 2*n)
	es := make([]Entity, n)
	for i := range es {
		es[i] = Entity{Kind: "bold", Offset: i, Length: 2*n - 2*i}
	}
	b.ReportAllocs()
	for b.Loop() {
		TextRuns(text, es)
	}
}
