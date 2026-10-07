// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"komarugram/internal/messenger/model"
)

func TestTextBlocksSelectionCopyAndCollapse(t *testing.T) {
	text := "Before 👋\ncode\n\nline two\nquote one\nquote two\nquote three\nquote four\nafter"
	codeStart := utf16Length("Before 👋\n")
	code := "code\n\nline two"
	quoteStart := codeStart + utf16Length(code) + 1
	quote := "quote one\nquote two\nquote three\nquote four"
	h := newInteractionHarness(t, model.TextRuns(text, []model.Entity{
		{Kind: "pre", Offset: codeStart, Length: utf16Length(code), Language: "go"},
		{Kind: "quote", Offset: quoteStart, Length: utf16Length(quote), Collapsed: true},
		{Kind: "bold", Offset: quoteStart + 6, Length: 3},
	}))
	var pre, q *messageTextBlock
	for i := range h.row.textBlocks {
		b := &h.row.textBlocks[i]
		if h.row.runs[b.first].Pre {
			pre = b
		}
		if h.row.runs[b.first].Quote {
			q = b
		}
	}
	if pre == nil || q == nil {
		t.Fatal("pre and quote were not laid out as blocks")
	}
	h.animate = false
	collapsed := h.row.text.size.Y
	visibleEnd := func() int {
		end := 0
		for _, f := range h.row.text.fragments {
			if f.Index >= q.first && f.Index < q.end {
				for _, c := range f.Clusters {
					end = max(end, c.End)
				}
			}
		}
		return end
	}
	if visibleEnd() >= q.runeStart+utf8.RuneCountInString(quote) {
		t.Fatal("collapsed quote exposes hidden lines")
	}
	q.action.click.Click()
	h.frame()
	if h.row.text.size.Y <= collapsed || visibleEnd() != q.runeStart+utf8.RuneCountInString(quote) {
		t.Fatal("quote did not expand and expose its last line")
	}
	// Pointer selection spans the paragraph, code plate and quote, retaining
	// the original line breaks and UTF-16 -> rune mapping.
	first := h.row.text.fragments[0].Bounds
	last := h.row.text.fragments[len(h.row.text.fragments)-1].Bounds
	h.pointer(pointer.Press, f32.Pt(float32(first.Min.X), float32(first.Min.Y+first.Dy()/2)))
	h.pointer(pointer.Drag, f32.Pt(float32(last.Max.X), float32(last.Min.Y+last.Dy()/2)))
	h.pointer(pointer.Release, f32.Pt(float32(last.Max.X), float32(last.Min.Y+last.Dy()/2)))
	if got := h.row.selectedText(); got != text {
		t.Fatalf("selected %q, want %q", got, text)
	}
	pre.action.click.Click()
	h.frame()
	_, copied, ok := h.router.WriteClipboard()
	if !ok || string(copied) != code {
		t.Fatalf("copy code: %q, %v", copied, ok)
	}
	q.action.click.Click()
	h.frame()
	if h.row.text.size.Y != collapsed {
		t.Fatal("quote did not collapse")
	}
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

func TestTextBlockCopyHonorsProtectionAndSpoilers(t *testing.T) {
	h := newInteractionHarness(t, model.TextRuns("safe secret", []model.Entity{
		{Kind: "pre", Length: 11}, {Kind: "spoiler", Offset: 5, Length: 6},
	}))
	b := &h.row.textBlocks[0]
	// Exercise the actual header hit area, not only a synthetic button click.
	h.pointer(pointer.Press, f32.Pt(220, 20))
	h.pointer(pointer.Release, f32.Pt(220, 20))
	_, copied, ok := h.router.WriteClipboard()
	if !ok || string(copied) != "safe [•••]" || h.page.activeText != nil {
		t.Fatalf("header click copied %q, ok %v, selected text %v", copied, ok, h.page.activeText != nil)
	}
	if got := h.row.copyTextBlock(b); got != "safe [•••]" {
		t.Fatalf("copy exposed spoiler: %q", got)
	}
	h.row.revealed = true
	if got := h.row.copyTextBlock(b); got != "safe secret" {
		t.Fatalf("revealed copy: %q", got)
	}
	h.row.noCopy = true
	b.action.click.Click()
	h.frame()
	if _, _, ok := h.router.WriteClipboard(); ok {
		t.Fatal("protected code copied")
	}
	h.row.text.anchor, h.row.text.caret = 0, 11
	if h.row.selectedText() != "" {
		t.Fatal("protected selection copied")
	}
}

func TestTextBlocksKeepLinkAndSpoilerHitCoordinates(t *testing.T) {
	h := newInteractionHarness(t, model.TextRuns("before\nlink secret", []model.Entity{
		{Kind: "quote", Offset: 7, Length: 11},
		{Kind: "url", Offset: 7, Length: 4, URL: "https://example.com"},
		{Kind: "spoiler", Offset: 12, Length: 6},
	}))
	h.animate = false
	click := func(kind string) {
		t.Helper()
		for _, f := range h.row.text.fragments {
			r := h.row.runs[f.Index]
			if kind == "link" && r.URL == "" || kind == "spoiler" && !r.Spoiler {
				continue
			}
			pos := f32.Pt(float32(f.Bounds.Min.X+f.Bounds.Dx()/2), float32(f.Bounds.Min.Y+f.Bounds.Dy()/2))
			h.pointer(pointer.Press, pos)
			h.pointer(pointer.Release, pos)
			return
		}
		t.Fatal("missing hit region")
	}
	click("link")
	if h.page.link != "https://example.com" {
		t.Fatal("quote link not activated")
	}
	h.now = h.now.Add(time.Second)
	click("spoiler")
	if !h.row.revealed {
		t.Fatal("quote spoiler not revealed")
	}
}

func TestCodeLanguageLabelHostile(t *testing.T) {
	if got := codeLanguageLabel("go\n\t\x00\u202e\u2066" + strings.Repeat("x", 100000)); strings.ContainsAny(got, "\n\t\x00\u202e\u2066") || utf8.RuneCountInString(got) > 40 {
		t.Fatalf("unsafe label: %q", got)
	}
}

func TestTextBlockWrappedCodeKeepsEveryGlyph(t *testing.T) {
	code := "func greet() {\n    fmt.Println(\"Привет, мир! 👋\")\n}"
	h := newInteractionHarness(t, model.TextRuns(code, []model.Entity{{Kind: "pre", Length: utf16Length(code), Language: "go"}}))
	for _, width := range []int{1, 160, 200, 240, 360} {
		h.size.X = width
		h.frame()
		last := 0
		for _, f := range h.row.text.fragments {
			for _, c := range f.Clusters {
				last = max(last, c.End)
			}
		}
		if last != utf8.RuneCountInString(code) {
			t.Fatalf("width %d: final code glyph missing: %d of %d", width, last, utf8.RuneCountInString(code))
		}
	}
}
