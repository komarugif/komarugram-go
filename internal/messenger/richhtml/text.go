// SPDX-License-Identifier: Unlicense OR MIT

package richhtml

import (
	"fmt"
	"html"
	"slices"
	"strings"

	"komarugram/internal/messenger/model"
)

// byteOffset is the byte of s at UTF-16 code unit at.
func byteOffset(s string, at int) int {
	units := 0
	for i, r := range s {
		if units >= at {
			return i
		}
		units++
		if r >= 0x10000 {
			units++
		}
	}
	return len(s)
}

// text writes t: its runs with their styles, an anchor inside it where it
// is.
func (w *writer) text(t model.RichText) {
	type anchor struct {
		at   int
		name string
	}
	var anchors []anchor
	for i, name := range t.Anchors {
		at := t.AnchorOffset(i)
		if at < 0 {
			at = 0
		}
		anchors = append(anchors, anchor{byteOffset(t.Text, at), name})
	}
	slices.SortStableFunc(anchors, func(a, b anchor) int { return a.at - b.at })
	// The text is cut at its anchors, each piece written on its own: an
	// entity across one is closed before it and opened again after.
	pos := 0
	for _, a := range anchors {
		if a.at > pos {
			w.runs(t.Slice(pos, a.at))
			pos = a.at
		}
		if name := model.AnchorName(a.name); name != "" {
			w.printf("<a id=\"%s\"></a>", attr(name))
		}
	}
	if pos == 0 {
		w.runs(t)
	} else if pos < len(t.Text) {
		w.runs(t.Slice(pos, len(t.Text)))
	}
}

// runs writes the runs of t.
func (w *writer) runs(t model.RichText) {
	text, entities := t.Text, t.Entities
	if w.o.Date != nil {
		text, entities = model.FormatDates(text, entities, w.o.Date)
	}
	for _, run := range model.TextRuns(text, entities) {
		w.run(run)
	}
}

// run writes a run, its styles each an element around it.
func (w *writer) run(run model.TextRun) {
	if run.Text == "" {
		return
	}
	if run.Math {
		if svg := w.formula(run.Text, false); svg != "" {
			w.printf("<span class=\"math\">%s</span>", svg)
			return
		}
	}
	if run.Spoiler && w.o.HideSpoilers {
		w.printf("[•••]")
		return
	}
	var closing []string
	open := func(tag, attrs string) {
		w.printf("<%s%s>", tag, attrs)
		closing = append(closing, "</"+tag+">")
	}
	if href := safeURL(run.URL); href != "" {
		open("a", fmt.Sprintf(` href="%s"`, attr(href)))
	} else if run.Button != nil {
		if href := safeURL(run.Button.URL); href != "" {
			open("a", fmt.Sprintf(` class="button" href="%s"`, attr(href)))
		} else {
			open("span", ` class="button"`)
		}
	}
	if run.Spoiler {
		open("span", ` class="spoiler" tabindex="0"`)
	}
	if run.Code {
		open("code", "")
	}
	if run.Bold {
		open("strong", "")
	}
	if run.Italic {
		open("em", "")
	}
	if run.Underline {
		open("u", "")
	}
	if run.Strike {
		open("s", "")
	}
	if run.Sub {
		open("sub", "")
	}
	if run.Sup {
		open("sup", "")
	}
	if run.Marked {
		open("mark", "")
	}
	lines := strings.Split(run.Text, "\n")
	for i, line := range lines {
		if i > 0 {
			w.printf("<br>\n")
		}
		w.printf("%s", html.EscapeString(line))
	}
	for i := len(closing) - 1; i >= 0; i-- {
		w.printf("%s", closing[i])
	}
}
