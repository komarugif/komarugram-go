// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"container/heap"
	"sort"
	"strings"
)

// TextRun is a non-overlapping interval with all nested styles applied.
type TextRun struct {
	Text, URL                                             string
	Bold, Italic, Code, Underline, Strike, Spoiler, Quote bool
	Emoji                                                 int64
	// Block identifies a pre or quote entity (one-based input index). Adjacent
	// blocks stay separate even when their styles and languages are identical.
	Block          int
	Pre, Collapsed bool
	Language       string
	// Action is what a click on the run does when it opens no URL: the kind
	// of its entity (hashtag, cashtag, bot_command, bank_card or date), with
	// Value, the entity's whole text, and Date, a date's Unix time.
	Action, Value string
	Date          int64
	// Sub, Sup and Marked are a rich text's subscript, superscript and
	// marked text.
	Sub, Sup, Marked bool
	// Math is a formula's LaTeX source, drawn as the formula once it is
	// laid out; Code is set too, for its source's font until then.
	Math bool
	// Button is the inline button of a rich text the run is the label of,
	// which a click presses; its Action is "button".
	Button *MessageButton
}

// actionKinds are the entities a click acts on without a URL.
var actionKinds = map[string]bool{"hashtag": true, "cashtag": true, "bot_command": true, "bank_card": true, "date": true}

// entityHeap gives later entities priority, matching Telegram entity order.
// Removed entries are discarded lazily, so overlapping hostile intervals
// take O(n log n) work instead of scanning every entity for every boundary.
type entityHeap []int

func (h entityHeap) Len() int           { return len(h) }
func (h entityHeap) Less(i, j int) bool { return h[i] > h[j] }
func (h entityHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *entityHeap) Push(v any)        { *h = append(*h, v.(int)) }
func (h *entityHeap) Pop() any          { a := *h; v := a[len(a)-1]; *h = a[:len(a)-1]; return v }
func (h *entityHeap) top(active []bool) int {
	for len(*h) > 0 && !active[(*h)[0]] {
		heap.Pop(h)
	}
	if len(*h) == 0 {
		return -1
	}
	return (*h)[0]
}

func TextRuns(text string, entities []Entity) []TextRun {
	if text == "" {
		return nil
	}
	// Only codepoint boundaries are valid UTF-16 offsets. Never split a surrogate pair.
	boundary := map[int]int{0: 0}
	units := 0
	for b, r := range text {
		boundary[units] = b
		units++
		if r > 0xffff {
			units++
		}
	}
	boundary[units] = len(text)
	type event struct {
		pos, index int
		start      bool
	}
	var events []event
	for i, e := range entities {
		// Check with subtraction before adding: data can also come from old or
		// damaged JSON caches, where int is wider than Telegram's int32.
		if e.Offset < 0 || e.Offset > units || e.Length <= 0 || e.Length > units-e.Offset {
			continue
		}
		if _, ok := boundary[e.Offset]; !ok {
			continue
		}
		if _, ok := boundary[e.Offset+e.Length]; !ok {
			continue
		}
		switch e.Kind {
		case "bold", "italic", "code", "pre", "underline", "strike", "spoiler", "quote", "emoji", "url", "mention", "email", "phone",
			"sub", "sup", "marked", "math", "button":
		default:
			if !actionKinds[e.Kind] {
				continue
			}
		}
		events = append(events, event{e.Offset, i, true}, event{e.Offset + e.Length, i, false})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })
	active := make([]bool, len(entities))
	counts := map[string]int{}
	var links, emojis, blocks entityHeap
	var out []TextRun
	pos, next := 0, 0
	for pos < units {
		for next < len(events) && events[next].pos == pos {
			ev := events[next]
			e := entities[ev.index]
			delta := -1
			active[ev.index] = ev.start
			if ev.start {
				delta = 1
				switch e.Kind {
				case "url", "mention", "email", "phone", "hashtag", "cashtag", "bot_command", "bank_card", "date", "button":
					heap.Push(&links, ev.index)
				case "emoji":
					heap.Push(&emojis, ev.index)
				case "pre", "quote":
					heap.Push(&blocks, ev.index)
				}
			}
			counts[e.Kind] += delta
			next++
		}
		end := units
		if next < len(events) {
			end = events[next].pos
		}
		run := TextRun{Text: text[boundary[pos]:boundary[end]], Bold: counts["bold"] > 0,
			// A formula ("math") shows its source in the code's font until
			// it is laid out.
			Italic: counts["italic"] > 0, Code: counts["code"] > 0 || counts["pre"] > 0 || counts["math"] > 0, Math: counts["math"] > 0,
			Underline: counts["underline"] > 0, Strike: counts["strike"] > 0, Spoiler: counts["spoiler"] > 0,
			Sub: counts["sub"] > 0, Sup: counts["sup"] > 0, Marked: counts["marked"] > 0}
		if i := blocks.top(active); i >= 0 {
			e := entities[i]
			run.Block, run.Pre, run.Quote = i+1, e.Kind == "pre", e.Kind == "quote"
			run.Language, run.Collapsed = e.Language, e.Collapsed
		}
		if i := emojis.top(active); i >= 0 {
			run.Emoji = entities[i].DocumentID
		}
		if i := links.top(active); i >= 0 {
			e := entities[i]
			value := text[boundary[e.Offset]:boundary[e.Offset+e.Length]]
			switch {
			case e.Kind == "mention":
				run.URL = "https://t.me/" + strings.TrimPrefix(value, "@")
			case e.Kind == "email":
				run.URL = "mailto:" + value
			case e.Kind == "phone":
				run.URL = "tel:" + value
			case actionKinds[e.Kind]:
				run.Action, run.Value, run.Date = e.Kind, value, e.Date
			case e.Kind == "button" && e.Button != nil:
				run.Action, run.Value, run.Button = e.Kind, value, e.Button
			default:
				run.URL = e.URL
				if run.URL == "" {
					run.URL = value
				}
			}
		}
		// Redundant nested styles must not create thousands of shaping calls.
		if len(out) > 0 {
			previous := out[len(out)-1]
			previous.Text = run.Text
			if previous == run {
				// Both slices refer to text. Extending the slice avoids quadratic
				// string concatenation when many redundant intervals overlap.
				out[len(out)-1].Text = text[boundary[pos]-len(out[len(out)-1].Text) : boundary[end]]
				pos = end
				continue
			}
		}
		out = append(out, run)
		pos = end
	}
	return out
}
