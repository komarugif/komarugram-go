// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"slices"
	"strings"
	"testing"
)

// The text is on one line, and the entities a list draws move with it;
// the others go.
func TestPreviewTextFoldsSpacesAndMovesEntities(t *testing.T) {
	// "  ab\n\n *cd*  🙂 e  ": a and b italic, cd bold (dropped), the space
	// before cd and cd struck, the emoji custom, e in a pre.
	text := "  ab\n\n cd  🙂 e  "
	entities := []Entity{
		{Kind: "italic", Offset: 2, Length: 2},
		{Kind: "bold", Offset: 7, Length: 2},
		{Kind: "strike", Offset: 6, Length: 3},
		{Kind: "emoji", Offset: 11, Length: 2, DocumentID: 9},
		{Kind: "pre", Offset: 14, Length: 1},
		{Kind: "url", Offset: 2, Length: 7, URL: "https://example.com"},
	}
	got, moved := PreviewText(text, entities)
	if got != "ab cd 🙂 e" {
		t.Fatalf("text %q", got)
	}
	want := []Entity{
		{Kind: "italic", Offset: 0, Length: 2},
		{Kind: "strike", Offset: 3, Length: 2},
		{Kind: "emoji", Offset: 6, Length: 2, DocumentID: 9},
		{Kind: "code", Offset: 9, Length: 1},
	}
	if !slices.Equal(moved, want) {
		t.Fatalf("entities\n got %+v\nwant %+v", moved, want)
	}
}

// A long text is cut, an ellipsis after it, and an entity over the cut
// ends there.
func TestPreviewTextCut(t *testing.T) {
	text := strings.Repeat("a", PreviewTextLimit-1) + "🙂" + strings.Repeat("b", 50)
	got, moved := PreviewText(text, []Entity{{Kind: "italic", Offset: PreviewTextLimit - 3, Length: 20}, {Kind: "spoiler", Offset: PreviewTextLimit + 5, Length: 3}})
	// The emoji, two units, would pass the limit: it is not split.
	if want := strings.Repeat("a", PreviewTextLimit-1) + "…"; got != want {
		t.Fatalf("text %q", got)
	}
	if want := []Entity{{Kind: "italic", Offset: PreviewTextLimit - 3, Length: 2}}; !slices.Equal(moved, want) {
		t.Fatalf("entities %+v", moved)
	}
}
