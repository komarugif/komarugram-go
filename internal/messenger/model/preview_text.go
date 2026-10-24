// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"strings"
	"unicode"
)

// PreviewTextLimit is how much of a message's text, in UTF-16 units, a
// preview keeps: more than a row of the widest list shows, and the dialogs
// the cache keeps stay small.
const PreviewTextLimit = 256

// previewEntity tells whether a list draws the entity of kind, and as
// what: Telegram Desktop's keeps italic, underline, strike, code (a pre
// drawn as code), spoilers and custom emoji; bold, links and the rest are
// plain text there.
func previewEntity(kind string) (string, bool) {
	switch kind {
	case "italic", "underline", "strike", "code", "spoiler", "emoji":
		return kind, true
	case "pre":
		return "code", true
	}
	return "", false
}

// PreviewText is text on one line, its spaces and line breaks one space
// each and none at its ends, cut after PreviewTextLimit with an ellipsis,
// and the entities of it a list draws, moved to where their text is now.
func PreviewText(text string, entities []Entity) (string, []Entity) {
	var kept []Entity
	for _, e := range entities {
		if kind, ok := previewEntity(e.Kind); ok && e.Length > 0 && e.Offset >= 0 {
			kept = append(kept, Entity{Kind: kind, Offset: e.Offset, Length: e.Length, DocumentID: e.DocumentID})
		}
	}
	// at maps the UTF-16 offset of each rune of text to the one it has in
	// out; a space folded into the one before maps to where that one ends.
	var out strings.Builder
	out.Grow(min(len(text), PreviewTextLimit*3))
	at := make(map[int]int, 2*len(kept))
	for _, e := range kept {
		at[e.Offset], at[e.Offset+e.Length] = -1, -1
	}
	in, units := 0, 0
	space, cut := false, false
	for _, r := range text {
		if _, ok := at[in]; ok {
			at[in] = units
			if space && units > 0 {
				// An entity starting after a space starts after the one kept.
				at[in] = units + 1
			}
		}
		n := utf16Len(r)
		in += n
		if unicode.IsSpace(r) {
			space = units > 0
			continue
		}
		if space {
			if units+1 > PreviewTextLimit {
				cut = true
				break
			}
			out.WriteByte(' ')
			units++
			space = false
		}
		if units+n > PreviewTextLimit {
			cut = true
			break
		}
		out.WriteRune(r)
		units += n
	}
	if _, ok := at[in]; ok && !cut {
		at[in] = units
	}
	result := out.String()
	if cut {
		result += "…"
	}
	var moved []Entity
	for _, e := range kept {
		start, ok1 := at[e.Offset]
		end, ok2 := at[e.Offset+e.Length]
		if !ok1 || !ok2 || start < 0 {
			continue
		}
		if end < 0 || end > units {
			// Cut, or past the text: to its end.
			end = units
		}
		if end > start {
			e.Offset, e.Length = start, end-start
			moved = append(moved, e)
		}
	}
	return result, moved
}
