// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"komarugram/internal/messenger/codehighlight"
	"komarugram/internal/messenger/model"
)

// A run is cut where the block's colors change, by the run's own offsets.
func TestCodePieces(t *testing.T) {
	spans := []codehighlight.Span{{Start: 0, End: 4, Class: codehighlight.Keyword}, {Start: 6, End: 9, Class: codehighlight.String}, {Start: 12, End: 20, Class: codehighlight.Comment}}
	for _, c := range []struct {
		text string
		base int
		want []codePiece
	}{
		{"func x", 0, []codePiece{{0, 4, codehighlight.Keyword}, {4, 6, codehighlight.Plain}}},
		{"x \"a\" ", 4, []codePiece{{0, 2, codehighlight.Plain}, {2, 5, codehighlight.String}, {5, 6, codehighlight.Plain}}},
		{"// note", 13, []codePiece{{0, 7, codehighlight.Comment}}},
		{"tail", 20, []codePiece{{0, 4, codehighlight.Plain}}},
	} {
		got := codePieces(c.text, c.base, spans)
		if len(got) != len(c.want) {
			t.Fatalf("%q: %v", c.text, got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q: %v, not %v", c.text, got, c.want)
			}
		}
	}
}

// A code block's colors come in the background and redraw the window; its
// runs are cut into more spans, which still select and copy as the text.
func TestCodeBlockColorsKeepSelection(t *testing.T) {
	text := "Код:\nfunc main() {\n\ts := \"привет\" // 👋\n}\nпосле"
	code := "func main() {\n\ts := \"привет\" // 👋\n}"
	start := utf16Length("Код:\n")
	runs := model.TextRuns(text, []model.Entity{
		{Kind: "pre", Offset: start, Length: utf16Length(code), Language: "go"},
		{Kind: "bold", Offset: start + 5, Length: 4},
	})
	var invalidated atomic.Bool
	h := &interactionHarness{page: newChatPage(benchmarkHistory{}, func() { invalidated.Store(true) }), now: time.Unix(1000, 0), size: image.Pt(320, 500), row: &messageRow{runs: runs}, animate: false}
	t.Cleanup(h.page.Close)
	h.frame()
	var pre *messageTextBlock
	for i := range h.row.textBlocks {
		if b := &h.row.textBlocks[i]; h.row.runs[b.first].Pre {
			pre = b
		}
	}
	codeFragments := func() int {
		n := 0
		for _, f := range h.row.text.fragments {
			if f.Index >= pre.first && f.Index < pre.end {
				n++
			}
		}
		return n
	}
	plain := codeFragments()
	deadline := time.Now().Add(10 * time.Second)
	for !pre.code.ready {
		if time.Now().After(deadline) {
			t.Fatal("no colors came")
		}
		time.Sleep(5 * time.Millisecond)
		h.frame()
	}
	if len(pre.code.spans) == 0 || !invalidated.Load() {
		t.Fatalf("colors %v, window redrawn %v", pre.code.spans, invalidated.Load())
	}
	if colored := codeFragments(); colored <= plain {
		t.Fatalf("%d spans of code with colors, %d without", colored, plain)
	}
	first := h.row.text.fragments[0].Bounds
	last := h.row.text.fragments[len(h.row.text.fragments)-1].Bounds
	h.pointer(pointer.Press, f32.Pt(float32(first.Min.X), float32(first.Min.Y+first.Dy()/2)))
	h.pointer(pointer.Drag, f32.Pt(float32(last.Max.X), float32(last.Min.Y+last.Dy()/2)))
	h.pointer(pointer.Release, f32.Pt(float32(last.Max.X), float32(last.Min.Y+last.Dy()/2)))
	if got := h.row.selectedText(); got != text {
		t.Fatalf("selected %q", got)
	}
	pre.action.click.Click()
	h.frame()
	if _, copied, ok := h.router.WriteClipboard(); !ok || string(copied) != code {
		t.Fatalf("copied %q, %v", copied, ok)
	}
}

// A tab in code is as wide as four spaces, where the fonts drew a box of
// one letter, and still selects and copies as a tab.
func TestCodeTabsAreFourSpaces(t *testing.T) {
	text := "x\ty\n    y"
	runs := model.TextRuns(text, []model.Entity{{Kind: "pre", Offset: 0, Length: utf16Length(text)}})
	h := &interactionHarness{page: newChatPage(benchmarkHistory{}, func() {}), now: time.Unix(1000, 0), size: image.Pt(320, 300), row: &messageRow{runs: runs}, animate: false}
	t.Cleanup(h.page.Close)
	h.frame()
	// The clusters of "x\ty" and of "    y": the y after the tab is where
	// the one after four spaces is, give or take the x.
	var tab, spaced []int
	for _, f := range h.row.text.fragments {
		for _, c := range f.Clusters {
			if c.Start == 2 {
				tab = append(tab, c.Bounds.Min.X)
			}
			if c.Start == 8 {
				spaced = append(spaced, c.Bounds.Min.X)
			}
		}
	}
	if len(tab) != 1 || len(spaced) != 1 {
		t.Fatalf("clusters of y: %v %v", tab, spaced)
	}
	if tab[0] < spaced[0] {
		t.Fatalf("y after the tab at %d, after four spaces at %d", tab[0], spaced[0])
	}
	first := h.row.text.fragments[0].Bounds
	last := h.row.text.fragments[len(h.row.text.fragments)-1].Bounds
	h.pointer(pointer.Press, f32.Pt(float32(first.Min.X), float32(first.Min.Y+first.Dy()/2)))
	h.pointer(pointer.Drag, f32.Pt(float32(last.Max.X), float32(last.Min.Y+last.Dy()/2)))
	h.pointer(pointer.Release, f32.Pt(float32(last.Max.X), float32(last.Min.Y+last.Dy()/2)))
	if got := h.row.selectedText(); got != text {
		t.Fatalf("selected %q", got)
	}
}
