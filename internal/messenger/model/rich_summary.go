// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"strconv"
	"strings"
	"unicode"
)

// UTF16Len is how many UTF-16 code units s takes, as entity offsets count
// them; an invalid byte counts as one, as in TextRuns.
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

// Append adds o to t, with its entities and anchors.
func (t *RichText) Append(o RichText) {
	shift := UTF16Len(t.Text)
	t.Text += o.Text
	for _, e := range o.Entities {
		e.Offset += shift
		t.Entities = append(t.Entities, e)
	}
	if len(o.Anchors) > 0 {
		t.padAnchors()
		for i, name := range o.Anchors {
			at := o.AnchorOffset(i)
			if at >= 0 {
				at += shift
			}
			t.Anchors = append(t.Anchors, name)
			t.AnchorAt = append(t.AnchorAt, at)
		}
	}
}

// Mark gives the text from offset from, in UTF-16 code units, to its end
// entity e, with that offset and length; nothing when it is empty.
func (t *RichText) Mark(from int, e Entity) {
	if n := UTF16Len(t.Text) - from; n > 0 {
		e.Offset, e.Length = from, n
		t.Entities = append(t.Entities, e)
	}
}

// Trimmed is t without the spaces it begins and ends with; its entities
// are cut to what is left.
func (t RichText) Trimmed() RichText {
	start := len(t.Text) - len(strings.TrimLeftFunc(t.Text, unicode.IsSpace))
	end := len(strings.TrimRightFunc(t.Text, unicode.IsSpace))
	if start == 0 && end == len(t.Text) {
		return t
	}
	if start >= end {
		return RichText{Anchors: t.Anchors}
	}
	return t.slice(start, end)
}

// Slice is bytes from to to of t's text, from and to at the starts of
// characters: its entities cut to what is left, its anchors kept, those
// outside at its ends.
func (t RichText) Slice(from, to int) RichText {
	from, to = min(max(from, 0), len(t.Text)), min(max(to, 0), len(t.Text))
	if from >= to {
		return RichText{}
	}
	return t.slice(from, to)
}

func (t RichText) slice(start, end int) RichText {
	lead := UTF16Len(t.Text[:start])
	length := UTF16Len(t.Text[start:end])
	out := RichText{Text: t.Text[start:end], Anchors: t.Anchors}
	if len(t.AnchorAt) > 0 {
		out.AnchorAt = make([]int, len(t.AnchorAt))
		for i, at := range t.AnchorAt {
			if at >= 0 {
				at = min(max(at-lead, 0), length)
			}
			out.AnchorAt[i] = at
		}
	}
	for _, e := range t.Entities {
		a := max(e.Offset, lead) - lead
		b := min(e.Offset+e.Length, lead+length) - lead
		if e.Offset < 0 || e.Length <= 0 || b <= a {
			continue
		}
		e.Offset, e.Length = a, b-a
		out.Entities = append(out.Entities, e)
	}
	return out
}

// Summary is the text of a rich message as a plain message shows it, a
// line for each block, as Telegram Desktop flattens it for the chat list,
// replies, notifications and search: list items begin with their marker,
// media give their captions. Headings stay bold, code blocks and quotes
// keep their entities. It is empty when nothing in the page is text; then
// Fallback tells what the page shows.
func (p RichPage) Summary() RichText {
	var s summary
	s.blocks(p.Blocks)
	return s.out.Trimmed()
}

type summary struct{ out RichText }

// line adds t as a line of its own, trimmed, unless nothing is left of it.
func (s *summary) line(t RichText) {
	t = t.Trimmed()
	if t.Text == "" {
		return
	}
	if s.out.Text != "" {
		s.out.Text += "\n"
	}
	s.out.Append(t)
}

func (s *summary) plain(text string) {
	s.line(RichText{Text: text})
}

func (s *summary) blocks(blocks []RichBlock) {
	for _, b := range blocks {
		s.block(b)
	}
}

// wrapped adds what add adds, as one entity e when it adds anything.
func (s *summary) wrapped(e Entity, add func()) {
	from := UTF16Len(s.out.Text)
	if s.out.Text != "" {
		// The line break before the first line belongs to the text before.
		from++
	}
	before := len(s.out.Text)
	add()
	if len(s.out.Text) > before {
		s.out.Mark(from, e)
	}
}

func (s *summary) block(b RichBlock) {
	switch b.Kind {
	case RichHeading:
		s.wrapped(Entity{Kind: "bold"}, func() { s.line(b.Text) })
	case RichParagraph, RichFooter, RichThinking:
		s.line(b.Text)
	case RichCode:
		s.wrapped(Entity{Kind: "pre", Language: b.Language}, func() { s.line(b.Text) })
	case RichAuthorDate, RichTable:
		s.line(b.Text)
	case RichButtons:
		for _, button := range b.Buttons {
			s.line(button.Text)
		}
	case RichList:
		s.list(b)
	case RichQuote:
		s.wrapped(Entity{Kind: "quote", Collapsed: b.Collapsed}, func() {
			s.line(b.Text)
			s.blocks(b.Blocks)
			s.line(b.Caption)
		})
	case RichMediaBlock, RichMap:
		s.line(b.Caption)
	case RichEmbed:
		if b.Caption.Text != "" {
			s.line(b.Caption)
		} else {
			s.plain(b.URL)
		}
	case RichEmbedPost:
		s.plain(b.Author)
		s.blocks(b.Blocks)
		s.line(b.Caption)
	case RichChannel:
		s.plain(b.Title)
	case RichMath:
		s.plain(b.Formula)
	case RichDetails:
		s.line(b.Text)
		s.blocks(b.Blocks)
	case RichRelated:
		s.line(b.Text)
		for _, a := range b.Related {
			s.plain(a.Title)
			s.plain(a.Description)
			s.plain(a.Author)
		}
	}
}

func (s *summary) list(b RichBlock) {
	number := b.listStart()
	step := 1
	if b.Reversed {
		step = -1
	}
	for _, item := range b.Items {
		if item.Value != nil {
			number = *item.Value
		}
		var line RichText
		switch {
		case item.Checkbox && item.Checked:
			line.Text = "[x] "
		case item.Checkbox:
			line.Text = "[ ] "
		case b.Ordered:
			if marker := orderedMarker(b, item, number); marker != "" {
				line.Text = marker + " "
			}
		default:
			line.Text = "- "
		}
		if item.Text.Text != "" {
			line.Append(item.Text)
		} else {
			var inner summary
			inner.blocks(item.Blocks)
			line.Append(inner.out)
		}
		s.line(line)
		number += step
	}
}

// ListMarkers are the markers of list b's items as an article shows them:
// an ordered item's number as orderedMarker writes it, "•" for the others,
// and "" for a checkbox, which is drawn.
func (b RichBlock) ListMarkers() []string {
	out := make([]string, len(b.Items))
	number := b.listStart()
	step := 1
	if b.Reversed {
		step = -1
	}
	for i, item := range b.Items {
		if item.Value != nil {
			number = *item.Value
		}
		switch {
		case item.Checkbox:
		case b.Ordered:
			out[i] = orderedMarker(b, item, number)
		default:
			out[i] = "•"
		}
		number += step
	}
	return out
}

// listStart is the number of an ordered list's first item.
func (b RichBlock) listStart() int {
	switch {
	case b.Start != nil:
		return *b.Start
	case b.Reversed:
		return len(b.Items)
	}
	return 1
}

// orderedMarker is how item, numbered n, is marked in ordered list b, as
// Telegram Desktop writes it: its own number as written, or n in the
// item's or the list's style, followed by a dot.
func orderedMarker(b RichBlock, item RichListItem, n int) string {
	if item.Num != "" {
		if strings.HasSuffix(item.Num, ".") || strings.HasSuffix(item.Num, ")") {
			return item.Num
		}
		return item.Num + "."
	}
	style := b.Type
	if item.Type != "" {
		style = item.Type
	}
	return markerBody(n, style) + "."
}

func markerBody(n int, style string) string {
	switch {
	case style == "a", strings.EqualFold(style, "lower-alpha"), strings.EqualFold(style, "lower-latin"):
		return alphaMarker(n, 'a')
	case style == "A", strings.EqualFold(style, "upper-alpha"), strings.EqualFold(style, "upper-latin"):
		return alphaMarker(n, 'A')
	case style == "i", strings.EqualFold(style, "lower-roman"):
		return strings.ToLower(romanMarker(n))
	case style == "I", strings.EqualFold(style, "upper-roman"):
		return romanMarker(n)
	}
	return strconv.Itoa(n)
}

func alphaMarker(n int, first byte) string {
	if n <= 0 {
		return strconv.Itoa(n)
	}
	var out []byte
	for n > 0 {
		n--
		out = append(out, first+byte(n%26))
		n /= 26
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func romanMarker(n int) string {
	if n <= 0 || n > 9999 {
		return strconv.Itoa(n)
	}
	parts := []struct {
		value int
		text  string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}}
	var out strings.Builder
	for _, p := range parts {
		for n >= p.value {
			out.WriteString(p.text)
			n -= p.value
		}
	}
	return out.String()
}

// Fallback tells what a page without text shows, for a summary to name it:
// "photo", "video", "album" (photos and videos), "audio", "file", "map" or
// "table", from its first such block; empty when it has none.
func (p RichPage) Fallback() string {
	for _, b := range p.Blocks {
		switch b.Kind {
		case RichMap:
			return "map"
		case RichTable:
			return "table"
		case RichMediaBlock:
			photos, videos := false, false
			for _, m := range b.Media {
				switch m.Kind {
				case MessagePhoto:
					photos = true
				case MessageVideo, MessageGIF:
					videos = true
				case MessageMusic, MessageVoice:
					if len(b.Media) == 1 {
						return "audio"
					}
				default:
					if len(b.Media) == 1 {
						return "file"
					}
				}
			}
			switch {
			case photos && videos:
				return "album"
			case videos:
				return "video"
			case photos:
				return "photo"
			}
		}
	}
	return ""
}
