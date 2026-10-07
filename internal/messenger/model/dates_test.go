// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"math"
	"testing"
)

// A formatted date's text is replaced, and the entities around it move,
// in UTF-16 units across characters beyond the BMP.
func TestFormatDatesMovesEntities(t *testing.T) {
	text := "👋 at 2026-10-05, bold #tag"
	units := UTF16Len(text)
	entities := []Entity{
		{Kind: "date", Offset: 6, Length: 10, Date: 1, DateFormat: DateShortDate},
		{Kind: "bold", Offset: 0, Length: units},
		// Ends inside the date: it ends before it.
		{Kind: "italic", Offset: 3, Length: 5},
		// Starts inside the date: it starts after it.
		{Kind: "underline", Offset: 10, Length: 8},
		// Wholly inside the date: it goes.
		{Kind: "strike", Offset: 7, Length: 3},
		{Kind: "hashtag", Offset: units - 4, Length: 4},
	}
	out, got := FormatDates(text, entities, func(Entity) string { return "5 окт" })
	if want := "👋 at 5 окт, bold #tag"; out != want {
		t.Fatalf("text %q, want %q", out, want)
	}
	want := []Entity{
		{Kind: "date", Offset: 6, Length: 5, Date: 1, DateFormat: DateShortDate},
		{Kind: "bold", Offset: 0, Length: UTF16Len(out)},
		{Kind: "italic", Offset: 3, Length: 3},
		{Kind: "underline", Offset: 11, Length: 2},
		{Kind: "hashtag", Offset: UTF16Len(out) - 4, Length: 4},
	}
	if len(got) != len(want) {
		t.Fatalf("entities %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entity %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	runs := TextRuns(out, got)
	if last := runs[len(runs)-1]; last.Action != "hashtag" || last.Value != "#tag" || !last.Bold {
		t.Fatalf("runs after the date: %+v", runs)
	}
}

// Dates without a format, overlapping dates, and ranges TextRuns would not
// take keep their text; nothing panics on hostile ranges.
func TestFormatDatesKeepsWhatItCannotFormat(t *testing.T) {
	text := "a👋bcdefgh"
	calls := 0
	format := func(Entity) string { calls++; return "X" }
	out, got := FormatDates(text, []Entity{
		{Kind: "date", Offset: 0, Length: 2, Date: 1},
		{Kind: "date", Offset: 2, Length: 1, Date: 1, DateFormat: DateShortTime},
		{Kind: "date", Offset: 3, Length: 3, Date: 1, DateFormat: DateShortTime},
		{Kind: "date", Offset: 4, Length: 3, Date: 1, DateFormat: DateShortTime},
		{Kind: "date", Offset: -1, Length: 3, DateFormat: DateShortTime},
		{Kind: "date", Offset: 8, Length: math.MaxInt, DateFormat: DateShortTime},
		{Kind: "date", Offset: 8, Length: 1, Date: 1, DateFormat: DateShortTime},
		{Kind: "bold", Offset: math.MaxInt, Length: 1},
	}, format)
	// The one in the surrogate pair is not a date's range; of the two that
	// overlap, the first is replaced.
	if out != "a👋XefXh" || calls != 2 {
		t.Fatalf("%q after %d calls", out, calls)
	}
	for _, e := range got {
		if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > UTF16Len(out) {
			t.Fatalf("entity out of range: %+v", e)
		}
	}
	if same, entities := FormatDates("abc", nil, format); same != "abc" || entities != nil {
		t.Fatal("text without dates changed")
	}
	if same, _ := FormatDates("abc", []Entity{{Kind: "date", Length: 3, DateFormat: DateShortTime}}, func(Entity) string { return "" }); same != "abc" {
		t.Fatal("a date the format left was replaced")
	}
}

// Clicks act on hashtags, cashtags, commands, cards and dates; email and
// phone numbers are links.
func TestTextRunsActions(t *testing.T) {
	text := "#go $TON /start@bot 4242 4242 4242 4242 x@y.z +1 555 0100 today"
	at := func(s string) (int, int) {
		for i := range text {
			if len(text[i:]) >= len(s) && text[i:i+len(s)] == s {
				return UTF16Len(text[:i]), UTF16Len(s)
			}
		}
		t.Fatalf("%q not in text", s)
		return 0, 0
	}
	var entities []Entity
	for _, e := range []struct{ kind, text string }{
		{"hashtag", "#go"}, {"cashtag", "$TON"}, {"bot_command", "/start@bot"}, {"bank_card", "4242 4242 4242 4242"},
		{"email", "x@y.z"}, {"phone", "+1 555 0100"}, {"date", "today"},
	} {
		off, n := at(e.text)
		entities = append(entities, Entity{Kind: e.kind, Offset: off, Length: n, Date: 42})
	}
	got := map[string]TextRun{}
	for _, r := range TextRuns(text, entities) {
		got[r.Text] = r
	}
	for _, c := range []struct{ text, action, url string }{
		{"#go", "hashtag", ""}, {"$TON", "cashtag", ""}, {"/start@bot", "bot_command", ""},
		{"4242 4242 4242 4242", "bank_card", ""}, {"today", "date", ""},
		{"x@y.z", "", "mailto:x@y.z"}, {"+1 555 0100", "", "tel:+1 555 0100"},
	} {
		r := got[c.text]
		if r.Action != c.action || r.URL != c.url || c.action != "" && r.Value != c.text {
			t.Errorf("%q: %+v", c.text, r)
		}
	}
	if got["today"].Date != 42 {
		t.Errorf("date lost: %+v", got["today"])
	}
}

// A rich text's subscripts, superscripts, marks, formulas and inline
// buttons come through as runs.
func TestTextRunsRichKinds(t *testing.T) {
	button := &MessageButton{Kind: "url", URL: "https://telegram.org"}
	runs := TextRuns("a b c d e", []Entity{
		{Kind: "sub", Offset: 0, Length: 1}, {Kind: "sup", Offset: 2, Length: 1}, {Kind: "marked", Offset: 4, Length: 1},
		{Kind: "math", Offset: 6, Length: 1}, {Kind: "button", Offset: 8, Length: 1, Button: button},
	})
	got := map[string]TextRun{}
	for _, r := range runs {
		got[r.Text] = r
	}
	if !got["a"].Sub || !got["b"].Sup || !got["c"].Marked || !got["d"].Code {
		t.Fatalf("styles lost: %+v", runs)
	}
	if e := got["e"]; e.Action != "button" || e.Button != button || e.Value != "e" {
		t.Fatalf("the button's run: %+v", e)
	}
	if got[" "].Sub || got[" "].Code {
		t.Fatalf("styles leaked: %+v", got[" "])
	}
}

// A list's markers are its items' numbers in its style, bullets, and none
// for checkboxes.
func TestListMarkers(t *testing.T) {
	three := 3
	b := RichBlock{Kind: RichList, Ordered: true, Start: &three, Type: "a", Items: []RichListItem{{}, {Checkbox: true}, {Num: "7)"}, {}}}
	if got := b.ListMarkers(); len(got) != 4 || got[0] != "c." || got[1] != "" || got[2] != "7)" || got[3] != "f." {
		t.Fatalf("ordered: %q", got)
	}
	b.Reversed, b.Start, b.Type = true, nil, ""
	if got := b.ListMarkers(); got[0] != "4." || got[3] != "1." {
		t.Fatalf("reversed: %q", got)
	}
	if got := (RichBlock{Kind: RichList, Items: []RichListItem{{}}}).ListMarkers(); got[0] != "•" {
		t.Fatalf("bullets: %q", got)
	}
}
