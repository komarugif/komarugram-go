// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"testing"
	"unicode/utf16"
)

func richText(s string) RichText { return RichText{Text: s} }

func ptr(n int) *int { return &n }

// Lists are flattened with Telegram Desktop's markers: the item's own number
// as written, or its number in the item's or the list's style.
func TestRichSummaryListMarkers(t *testing.T) {
	item := func(s string) RichListItem { return RichListItem{Text: richText(s)} }
	for _, c := range []struct {
		name string
		list RichBlock
		want string
	}{
		{"bullets and tasks", RichBlock{Kind: RichList, Items: []RichListItem{item("a"), {Checkbox: true, Text: richText("b")}, {Checkbox: true, Checked: true, Text: richText("c")}}}, "- a\n[ ] b\n[x] c"},
		{"decimal", RichBlock{Kind: RichList, Ordered: true, Items: []RichListItem{item("a"), item("b")}}, "1. a\n2. b"},
		{"start and value", RichBlock{Kind: RichList, Ordered: true, Start: ptr(5), Items: []RichListItem{item("a"), {Value: ptr(10), Text: richText("b")}, item("c")}}, "5. a\n10. b\n11. c"},
		{"reversed", RichBlock{Kind: RichList, Ordered: true, Reversed: true, Items: []RichListItem{item("a"), item("b"), item("c")}}, "3. a\n2. b\n1. c"},
		{"alpha", RichBlock{Kind: RichList, Ordered: true, Type: "a", Start: ptr(26), Items: []RichListItem{item("z"), item("aa")}}, "z. z\naa. aa"},
		{"upper alpha", RichBlock{Kind: RichList, Ordered: true, Type: "A", Items: []RichListItem{item("a")}}, "A. a"},
		{"roman", RichBlock{Kind: RichList, Ordered: true, Type: "upper-roman", Start: ptr(1994), Items: []RichListItem{item("a"), {Type: "i", Text: richText("b")}}}, "MCMXCIV. a\nmcmxcv. b"},
		{"roman out of range", RichBlock{Kind: RichList, Ordered: true, Type: "i", Start: ptr(0), Items: []RichListItem{item("a")}}, "0. a"},
		{"own numbers", RichBlock{Kind: RichList, Ordered: true, Items: []RichListItem{{Num: "7)", Text: richText("a")}, {Num: "B", Text: richText("b")}}}, "7) a\nB. b"},
		{"blocks", RichBlock{Kind: RichList, Items: []RichListItem{{Blocks: []RichBlock{{Kind: RichParagraph, Text: richText("one")}, {Kind: RichParagraph, Text: richText("two")}}}}}, "- one\ntwo"},
	} {
		if got := (RichPage{Blocks: []RichBlock{c.list}}).Summary().Text; got != c.want {
			t.Errorf("%s: %q, not %q", c.name, got, c.want)
		}
	}
}

// Trimming cuts entities to what is left of the text, and counts UTF-16
// code units, surrogate pairs too.
func TestRichTextTrimmed(t *testing.T) {
	in := RichText{Text: "  😀 bold  ", Entities: []Entity{
		{Kind: "bold", Offset: 0, Length: 11},
		{Kind: "italic", Offset: 5, Length: 4},
		{Kind: "code", Offset: 9, Length: 2},
	}}
	got := in.Trimmed()
	if got.Text != "😀 bold" {
		t.Fatalf("text %q", got.Text)
	}
	units := utf16.Encode([]rune(got.Text))
	want := map[string]string{"bold": "😀 bold", "italic": "bold"}
	if len(got.Entities) != len(want) {
		t.Fatalf("entities %+v", got.Entities)
	}
	for _, e := range got.Entities {
		if s := string(utf16.Decode(units[e.Offset : e.Offset+e.Length])); s != want[e.Kind] {
			t.Errorf("%s over %q", e.Kind, s)
		}
	}
	if got := (RichText{Text: " \n "}).Trimmed(); got.Text != "" || got.Entities != nil {
		t.Fatalf("blank %+v", got)
	}
}

// Each block gives its lines; blocks without text give none, and the page
// tells what it shows instead.
func TestRichSummaryBlocks(t *testing.T) {
	page := RichPage{Blocks: []RichBlock{
		{Kind: RichHeading, Level: 2, Text: richText(" Title ")},
		{Kind: RichDivider},
		{Kind: RichMediaBlock, Media: []RichMedia{{Kind: MessagePhoto}}},
		{Kind: RichCode, Language: "go", Text: richText("x := 1")},
		{Kind: RichQuote, Blocks: []RichBlock{{Kind: RichParagraph, Text: richText("inner")}}, Caption: richText("cap")},
		{Kind: RichDetails, Text: richText("More"), Blocks: []RichBlock{{Kind: RichParagraph, Text: richText("hidden")}}},
		{Kind: RichEmbed, URL: "https://e.org"},
		{Kind: RichRelated, Related: []RichArticle{{Title: "Other", Author: "Ann"}}},
	}}
	got := page.Summary()
	if got.Text != "Title\nx := 1\ninner\ncap\nMore\nhidden\nhttps://e.org\nOther\nAnn" {
		t.Fatalf("summary %q", got.Text)
	}
	spans := map[string]string{}
	for _, e := range got.Entities {
		spans[e.Kind] = got.Text[e.Offset : e.Offset+e.Length]
	}
	if spans["bold"] != "Title" || spans["pre"] != "x := 1" || spans["quote"] != "inner\ncap" {
		t.Fatalf("entities %v", spans)
	}
	for kind, page := range map[string]RichPage{
		"photo": {Blocks: []RichBlock{{Kind: RichMediaBlock, Media: []RichMedia{{Kind: MessagePhoto}, {Kind: MessagePhoto}}}}},
		"album": {Blocks: []RichBlock{{Kind: RichMediaBlock, Media: []RichMedia{{Kind: MessagePhoto}, {Kind: MessageVideo}}}}},
		"audio": {Blocks: []RichBlock{{Kind: RichDivider}, {Kind: RichMediaBlock, Media: []RichMedia{{Kind: MessageMusic}}}}},
		"file":  {Blocks: []RichBlock{{Kind: RichMediaBlock, Media: []RichMedia{{Kind: MessageFile}}}}},
		"table": {Blocks: []RichBlock{{Kind: RichTable}}},
		"":      {Blocks: []RichBlock{{Kind: RichDivider}}},
	} {
		if got := page.Fallback(); got != kind {
			t.Errorf("fallback %q, not %q", got, kind)
		}
	}
}

// Anchors keep where they are in a text through Append and Trimmed.
func TestAnchorOffsets(t *testing.T) {
	var a RichText
	a.Append(RichText{Text: "  ab"})
	a.AddAnchor("x")
	b := RichText{Text: "cd", Anchors: []string{"old"}}
	b.AddAnchor("y")
	a.Append(b)
	if a.AnchorOffset(0) != 4 || a.AnchorOffset(1) != -1 || a.AnchorOffset(2) != 6 {
		t.Fatalf("offsets %v of %v", a.AnchorAt, a.Anchors)
	}
	if tr := a.Trimmed(); tr.AnchorOffset(0) != 2 || tr.AnchorOffset(2) != 4 {
		t.Fatalf("trimmed offsets %v", tr.AnchorAt)
	}
}
