// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// FormatDates writes each formatted date of text as format has it, as
// Telegram Desktop does with a date entity that has a format: the entity's
// text is replaced, and the entities after it move. format returns "" for
// a date that keeps its text. A date that overlaps one before it keeps its
// text too.
//
// An entity that ends inside a replaced date ends before it, one that
// starts inside it starts after it, and one wholly inside it goes. Entities
// with ranges TextRuns would not take are dropped.
func FormatDates(text string, entities []Entity, format func(Entity) string) (string, []Entity) {
	if format == nil {
		return text, entities
	}
	var dates []int
	for i, e := range entities {
		if e.Kind == "date" && e.DateFormat != 0 {
			dates = append(dates, i)
		}
	}
	if len(dates) == 0 {
		return text, entities
	}
	// bytes maps a UTF-16 offset to its byte offset, -1 inside a surrogate
	// pair.
	units := UTF16Len(text)
	bytes := make([]int, units+1)
	u := 0
	for b, r := range text {
		bytes[u] = b
		u++
		if r > 0xffff {
			bytes[u] = -1
			u++
		}
	}
	bytes[units] = len(text)
	valid := func(e Entity) bool {
		return e.Offset >= 0 && e.Offset <= units && e.Length > 0 && e.Length <= units-e.Offset &&
			bytes[e.Offset] >= 0 && bytes[e.Offset+e.Length] >= 0
	}
	type replacement struct {
		start, end int
		text       string
		// shift is how much the text before this replacement moved.
		shift int
	}
	var reps []replacement
	sort.SliceStable(dates, func(i, j int) bool { return entities[dates[i]].Offset < entities[dates[j]].Offset })
	for _, i := range dates {
		e := entities[i]
		if !valid(e) || len(reps) > 0 && e.Offset < reps[len(reps)-1].end {
			continue
		}
		formatted := strings.ToValidUTF8(format(e), string(utf8.RuneError))
		if formatted == "" {
			continue
		}
		reps = append(reps, replacement{start: e.Offset, end: e.Offset + e.Length, text: formatted})
	}
	if len(reps) == 0 {
		return text, entities
	}
	var out strings.Builder
	at, shift := 0, 0
	for i := range reps {
		r := &reps[i]
		r.shift = shift
		out.WriteString(text[bytes[at]:bytes[r.start]])
		out.WriteString(r.text)
		at = r.end
		shift += UTF16Len(r.text) - (r.end - r.start)
	}
	out.WriteString(text[bytes[at]:])
	// moved is where offset pos goes; start says whether it starts an
	// entity, for a pos inside a replaced date.
	moved := func(pos int, start bool) int {
		i := sort.Search(len(reps), func(i int) bool { return reps[i].start >= pos })
		if i == 0 {
			return pos
		}
		r := reps[i-1]
		if pos < r.end {
			if start {
				return r.start + r.shift + UTF16Len(r.text)
			}
			return r.start + r.shift
		}
		return pos + r.shift + UTF16Len(r.text) - (r.end - r.start)
	}
	result := make([]Entity, 0, len(entities))
	for _, e := range entities {
		if !valid(e) {
			continue
		}
		start, end := moved(e.Offset, true), moved(e.Offset+e.Length, false)
		if end <= start {
			continue
		}
		e.Offset, e.Length = start, end-start
		result = append(result, e)
	}
	return out.String(), result
}
