// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"gioui.org/gesture"
	"github.com/go-text/typesetting/segmenter"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/styledtext"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// A spoiler opens with a circular wave from the click that moves at one
// speed whatever the size of what it covers: with one duration for all, a
// short message would open at a crawl and a long one in a rush. The duration
// is bounded both ways: below, only by a few frames, so that a word still
// shows the wave — any longer and a word opens at a crawl again — and above,
// so that a long message does not keep the reader waiting.
const (
	spoilerSpeed       = 300 // dp per second
	spoilerMinDuration = 120 * time.Millisecond
	spoilerMaxDuration = 1500 * time.Millisecond
)

type spoilerReveal struct {
	started time.Time
	center  f32.Point
	// pxPerDp is the scale the wave's speed is in; 0 counts as 1.
	pxPerDp float32
}

// reach is how far the wave goes to uncover every corner of size.
func (s spoilerReveal) reach(size image.Point) float64 {
	dx := max(math.Abs(float64(s.center.X)), math.Abs(float64(size.X)-float64(s.center.X)))
	dy := max(math.Abs(float64(s.center.Y)), math.Abs(float64(size.Y)-float64(s.center.Y)))
	return math.Hypot(dx, dy)
}

// duration is how long uncovering size takes.
func (s spoilerReveal) duration(size image.Point) time.Duration {
	scale := float64(s.pxPerDp)
	if scale <= 0 {
		scale = 1
	}
	d := time.Duration(s.reach(size) / (spoilerSpeed * scale) * float64(time.Second))
	return min(max(d, spoilerMinDuration), spoilerMaxDuration)
}

// done reports whether the wave has uncovered all of size by now.
func (s spoilerReveal) done(now time.Time, size image.Point) bool {
	return now.Sub(s.started) >= s.duration(size)
}

func (s spoilerReveal) radius(now time.Time, size image.Point) float32 {
	progress := min(1., max(0., float64(now.Sub(s.started))/float64(s.duration(size))))
	return float32(s.reach(size) * progress)
}

type textInteraction struct {
	fragments          []styledtext.Fragment
	size               image.Point
	anchor, caret      int
	pressed, dragged   bool
	clicker            gesture.Click
	dragger            gesture.Drag
	clicks             int
	anchorLo, anchorHi int
	origin             f32.Point
	reveal             spoilerReveal
}

func (s *textInteraction) hit(pos f32.Point) int {
	best, distance := 0, math.Inf(1)
	for _, f := range s.fragments {
		for _, c := range f.Clusters {
			// Prefer the nearest line, then the nearest cluster on that line.
			dy := max(float64(c.Bounds.Min.Y)-float64(pos.Y), max(0, float64(pos.Y)-float64(c.Bounds.Max.Y)))
			dx := max(float64(c.Bounds.Min.X)-float64(pos.X), max(0, float64(pos.X)-float64(c.Bounds.Max.X)))
			d := dy*10000 + dx
			if d < distance {
				distance = d
				after := pos.X >= float32(c.Bounds.Min.X+c.Bounds.Max.X)/2
				if c.RTL {
					after = !after
				}
				best = c.Start
				if after {
					best = c.End
				}
			}
		}
	}
	return best
}

func (s *textInteraction) fragmentAt(pos f32.Point) (styledtext.Fragment, bool) {
	for _, f := range s.fragments {
		if image.Pt(int(pos.X), int(pos.Y)).In(f.Bounds) {
			return f, true
		}
	}
	return styledtext.Fragment{}, false
}

func (p *chatPage) textEvents(gtx layout.Context, r *messageRow, animate bool) {
	s := &r.text
	// Use the same click/drag recognizers as widget.Selectable. In particular,
	// a press alone must not grab the pointer away from ancestor scrolling.
	var activation *gesture.ClickEvent
	for {
		e, ok := s.clicker.Update(gtx.Source)
		if !ok {
			break
		}
		switch e.Kind {
		case gesture.KindPress:
			if p.activeText != nil && p.activeText != r {
				p.activeText.text.anchor, p.activeText.text.caret = 0, 0
			}
			p.activeText = r
			pos := f32.Pt(float32(e.Position.X), float32(e.Position.Y))
			s.pressed, s.dragged, s.origin, s.clicks = true, false, pos, e.NumClicks
			caret := s.hit(pos)
			if !e.Modifiers.Contain(key.ModShift) {
				s.anchor = caret
			}
			s.caret = caret
			switch {
			case e.NumClicks == 2:
				s.anchor, s.caret = r.wordAt(caret)
			case e.NumClicks >= 3:
				s.anchor, s.caret = r.logicalLineAt(caret)
			}
			s.anchorLo, s.anchorHi = min(s.anchor, s.caret), max(s.anchor, s.caret)
			gtx.Execute(key.FocusCmd{Tag: &p.keyboard})
		case gesture.KindClick:
			activation = &e
		case gesture.KindCancel:
			// Drag's grab cancels Click. Drag still owns and completes the selection.
		}
	}
	for {
		e, ok := s.dragger.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch e.Kind {
		case pointer.Drag, pointer.Release:
			if !s.pressed {
				continue
			}
			if math.Hypot(float64(e.Position.X-s.origin.X), float64(e.Position.Y-s.origin.Y)) > float64(gtx.Dp(3)) {
				s.dragged = true
			}
			if e.Source == pointer.Mouse && s.dragged {
				caret := s.hit(e.Position)
				lo, hi := caret, caret
				if s.clicks == 2 {
					lo, hi = r.wordAt(caret)
				}
				if s.clicks >= 3 {
					lo, hi = r.logicalLineAt(caret)
				}
				if caret < s.anchorLo {
					s.anchor, s.caret = s.anchorHi, lo
				} else {
					s.anchor, s.caret = s.anchorLo, hi
				}
			}
			if e.Kind == pointer.Release {
				s.pressed = false
			}
		case pointer.Cancel:
			s.pressed = false
		}
	}
	if activation != nil && !s.dragged && activation.NumClicks == 1 && s.anchor == s.caret {
		pos := f32.Pt(float32(activation.Position.X), float32(activation.Position.Y))
		f, hit := s.fragmentAt(pos)
		first, initialHit := s.fragmentAt(s.origin)
		if hit && initialHit && first.Index == f.Index {
			run := r.runs[f.Index]
			if run.Spoiler && !r.revealed {
				if s.reveal.started.IsZero() {
					s.reveal = spoilerReveal{started: gtx.Now, center: s.origin, pxPerDp: gtx.Metric.PxPerDp}
				}
				if !animate {
					r.revealed = true
				}
			} else if run.URL != "" || run.Action != "" {
				p.activateRun(gtx, r, run)
			}
		}
	}

	if !s.reveal.started.IsZero() && !r.revealed {
		if !animate || s.reveal.done(gtx.Now, s.size) {
			r.revealed = true
		} else {
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
			if p.trace != nil {
				p.trace.Recorder.Count(p.trace.Window, "history.invalidate.spoiler")
			}
		}
	}
}

func (r *messageRow) selectedText() string {
	if r.noCopy {
		return ""
	}

	lo, hi := min(r.text.anchor, r.text.caret), max(r.text.anchor, r.text.caret)
	var result strings.Builder
	offset := 0
	for _, run := range r.runs {
		runes := []rune(run.Text)
		start, end := max(0, lo-offset), min(len(runes), hi-offset)
		if start < end {
			if run.Spoiler && !r.revealed {
				result.WriteString("[•••]")
			} else {
				result.WriteString(string(runes[start:end]))
			}
		}
		offset += len(runes)
	}
	return result.String()
}

func (p *chatPage) keyboardEvents(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: &p.keyboard},
			key.Filter{Focus: &p.keyboard, Name: "C", Required: key.ModShortcut},
			key.Filter{Focus: &p.keyboard, Name: "A", Required: key.ModShortcut},
			key.Filter{Focus: &p.keyboard, Name: "С", Required: key.ModShortcut},
			key.Filter{Focus: &p.keyboard, Name: "Ф", Required: key.ModShortcut},
			key.Filter{Focus: &p.keyboard, Name: key.NameLeftArrow, Optional: key.ModShift | key.ModShortcutAlt},
			key.Filter{Focus: &p.keyboard, Name: key.NameRightArrow, Optional: key.ModShift | key.ModShortcutAlt},
			key.Filter{Focus: &p.keyboard, Name: key.NameUpArrow, Optional: key.ModShift},
			key.Filter{Focus: &p.keyboard, Name: key.NameDownArrow, Optional: key.ModShift},
			key.Filter{Focus: &p.keyboard, Name: key.NameHome, Optional: key.ModShift | key.ModShortcut},
			key.Filter{Focus: &p.keyboard, Name: key.NameEnd, Optional: key.ModShift | key.ModShortcut},
			key.Filter{Focus: &p.keyboard, Name: key.NameEscape},
		)
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		if e.Name == key.NameEscape {
			if p.messageMenu.open || p.entityMenu.open {
				// Escape closes the menu first, as it does in Telegram Desktop.
				p.closeMenu()
				continue
			}
			p.clearSelection()
			p.activeText = nil
			continue
		}
		if p.activeText == nil {
			continue
		}
		r := p.activeText
		switch e.Name {
		case "C", "С":
			copySelection(gtx, r)
		case "A", "Ф":
			r.text.anchor, r.text.caret = 0, 0
			for _, run := range r.runs {
				r.text.caret += len([]rune(run.Text))
			}
		default:
			r.moveSelection(e)
		}
	}
	// A keyboard-only tag still adds a hit area to Gio. Keep it empty so it
	// cannot cover controls laid out earlier elsewhere in the window.
	area := clip.Rect(image.Rectangle{}).Push(gtx.Ops)
	event.Op(gtx.Ops, &p.keyboard)
	area.Pop()
}

func paintSpoiler(gtx layout.Context, size image.Point, center f32.Point, radius float32, col color.NRGBA, draw func()) {
	defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(3)).Push(gtx.Ops).Pop()
	if radius <= 0 {
		paint.Fill(gtx.Ops, col)
		return
	}
	// A clockwise rectangle and counter-clockwise circle form a cover with a
	// circular hole. Both text and bitmap emoji are revealed by the same clip.
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(0, 0))
	path.LineTo(f32.Pt(float32(size.X), 0))
	path.LineTo(f32.Pt(float32(size.X), float32(size.Y)))
	path.LineTo(f32.Pt(0, float32(size.Y)))
	path.Close()
	spoilerCircle(&path, center, radius)
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: path.End()}.Op())
	var inner clip.Path
	inner.Begin(gtx.Ops)
	spoilerCircle(&inner, center, radius)
	circle := clip.Outline{Path: inner.End()}.Op().Push(gtx.Ops)
	draw()
	circle.Pop()
}

// A counter-clockwise circle, shared by the cover hole and text clip.
func spoilerCircle(path *clip.Path, center f32.Point, radius float32) {
	const k = float32(0.5522847498)
	x, y, r := center.X, center.Y, radius
	path.MoveTo(f32.Pt(x+r, y))
	path.CubeTo(f32.Pt(x+r, y-r*k), f32.Pt(x+r*k, y-r), f32.Pt(x, y-r))
	path.CubeTo(f32.Pt(x-r*k, y-r), f32.Pt(x-r, y-r*k), f32.Pt(x-r, y))
	path.CubeTo(f32.Pt(x-r, y+r*k), f32.Pt(x-r*k, y+r), f32.Pt(x, y+r))
	path.CubeTo(f32.Pt(x+r*k, y+r), f32.Pt(x+r, y+r*k), f32.Pt(x+r, y))
	path.Close()
}

func (r *messageRow) plainRunes() []rune {
	var text strings.Builder
	for _, run := range r.runs {
		text.WriteString(run.Text)
	}
	return []rune(text.String())
}
func (r *messageRow) wordAt(caret int) (int, int) {
	text := r.plainRunes()
	if len(text) == 0 {
		return 0, 0
	}
	caret = min(caret, len(text)-1)
	var seg segmenter.Segmenter
	seg.Init(text)
	words := seg.WordIterator()
	for words.Next() {
		word := words.Word()
		if caret >= word.Offset && caret < word.Offset+len(word.Text) {
			return word.Offset, word.Offset + len(word.Text)
		}
	}
	// Whitespace can be selected as a unit too; punctuation/emoji retain their
	// shaped cluster boundary instead of splitting a combining sequence.
	if unicode.IsSpace(text[caret]) {
		lo, hi := caret, caret+1
		for lo > 0 && unicode.IsSpace(text[lo-1]) && text[lo-1] != '\n' {
			lo--
		}
		for hi < len(text) && unicode.IsSpace(text[hi]) && text[hi] != '\n' {
			hi++
		}
		return lo, hi
	}
	for _, f := range r.text.fragments {
		for _, c := range f.Clusters {
			if c.Start <= caret && c.End > caret {
				return c.Start, c.End
			}
		}
	}
	return caret, caret + 1
}

// logicalLineAt selects between explicit source-text breaks. A wrapped
// paragraph remains one unit for triple-click and triple-click dragging.
// Home/End and vertical navigation still use visual lines, like an editor.
func (r *messageRow) logicalLineAt(caret int) (int, int) {
	text := r.plainRunes()
	caret = max(0, min(caret, len(text)))
	isBreak := func(ch rune) bool {
		return ch == '\n' || ch == '\r' || ch == '\u2028' || ch == '\u2029'
	}
	// CRLF is one separator, never a selectable half of a newline.
	if caret > 0 && caret < len(text) && text[caret] == '\n' && text[caret-1] == '\r' {
		caret--
	}
	start, end := caret, caret
	for start > 0 && !isBreak(text[start-1]) {
		start--
	}
	for end < len(text) && !isBreak(text[end]) {
		end++
	}
	return start, end
}

func (s *textInteraction) lineAt(pos f32.Point) (int, int) {
	lo, hi, y := 0, 0, 0
	distance := math.Inf(1)
	for _, f := range s.fragments {
		d := math.Abs(float64(pos.Y) - float64(f.Bounds.Min.Y+f.Bounds.Max.Y)/2)
		if d < distance {
			distance, y = d, f.Bounds.Min.Y
		}
	}
	first := true
	for _, f := range s.fragments {
		if f.Bounds.Min.Y == y {
			for _, c := range f.Clusters {
				if first {
					lo, hi, first = c.Start, c.End, false
				} else {
					lo, hi = min(lo, c.Start), max(hi, c.End)
				}
			}
		}
	}
	return lo, hi
}

// Merge cluster rectangles before painting: overlapping translucent glyph
// rectangles otherwise produce a dark seam between every pair of letters.
func (s *textInteraction) selectionRegions() []image.Rectangle {
	lo, hi := min(s.anchor, s.caret), max(s.anchor, s.caret)
	var regions []image.Rectangle
	heights := map[int]int{}
	for _, f := range s.fragments {
		heights[f.Bounds.Min.Y] = max(heights[f.Bounds.Min.Y], f.Bounds.Max.Y)
	}
	for _, f := range s.fragments {
		for _, c := range f.Clusters {
			if c.Start < hi && c.End > lo && c.Bounds.Dx() > 0 {
				rect := c.Bounds
				rect.Min.Y = f.Bounds.Min.Y
				rect.Max.Y = heights[f.Bounds.Min.Y]
				regions = append(regions, rect)
			}
		}
	}
	sort.Slice(regions, func(i, j int) bool {
		if regions[i].Min.Y != regions[j].Min.Y {
			return regions[i].Min.Y < regions[j].Min.Y
		}
		return regions[i].Min.X < regions[j].Min.X
	})
	merged := regions[:0]
	for _, rect := range regions {
		n := len(merged)
		if n > 0 && merged[n-1].Min.Y == rect.Min.Y && rect.Min.X <= merged[n-1].Max.X+1 {
			merged[n-1] = merged[n-1].Union(rect)
		} else {
			merged = append(merged, rect)
		}
	}
	return merged
}
func (r *messageRow) moveSelection(e key.Event) {
	s := &r.text
	caret := s.caret
	direction := 1
	if e.Name == key.NameLeftArrow || e.Name == key.NameUpArrow {
		direction = -1
	}
	switch e.Name {
	case key.NameLeftArrow, key.NameRightArrow:
		if !e.Modifiers.Contain(key.ModShift) && s.anchor != s.caret {
			if direction < 0 {
				caret = min(s.anchor, s.caret)
			} else {
				caret = max(s.anchor, s.caret)
			}
		} else if e.Modifiers.Contain(key.ModShortcutAlt) {
			text := r.plainRunes()
			if direction < 0 {
				caret = max(0, caret-1)
				for caret > 0 && unicode.IsSpace(text[caret]) {
					caret--
				}
				caret, _ = r.wordAt(caret)
			} else {
				if caret < len(text) {
					_, caret = r.wordAt(caret)
				}
				for caret < len(text) && unicode.IsSpace(text[caret]) {
					caret++
				}
			}
		} else {
			next := caret
			for _, f := range s.fragments {
				for _, c := range f.Clusters {
					for _, boundary := range []int{c.Start, c.End} {
						if direction < 0 && boundary < caret && (next == caret || boundary > next) {
							next = boundary
						}
						if direction > 0 && boundary > caret && (next == caret || boundary < next) {
							next = boundary
						}
					}
				}
			}
			caret = next
		}
	case key.NameHome, key.NameEnd, key.NameUpArrow, key.NameDownArrow:
		var pos f32.Point
		height := float32(1)
		found := false
		for _, f := range s.fragments {
			for _, c := range f.Clusters {
				if c.Start == s.caret || (!found && c.End == s.caret) {
					x := c.Bounds.Min.X
					if (c.End == s.caret) != c.RTL {
						x = c.Bounds.Max.X
					}
					pos = f32.Pt(float32(x), float32(f.Bounds.Min.Y+f.Bounds.Max.Y)/2)
					height = float32(f.Bounds.Dy())
					found = true
				}
			}
		}
		switch e.Name {
		case key.NameHome:
			caret, _ = s.lineAt(pos)
			if e.Modifiers.Contain(key.ModShortcut) {
				caret = 0
			}
		case key.NameEnd:
			_, caret = s.lineAt(pos)
			if e.Modifiers.Contain(key.ModShortcut) {
				caret = len(r.plainRunes())
			}
		default:
			pos.Y += float32(direction) * height
			caret = s.hit(pos)
		}
	}
	s.caret = caret
	if !e.Modifiers.Contain(key.ModShift) {
		s.anchor = caret
	}
}

// actsOn reports whether a click on run does something, as textEvents
// acts on it: a link, an entity's action, an inline button, a spoiler not
// revealed.
func (r *messageRow) actsOn(run model.TextRun) bool {
	return run.URL != "" || run.Action != "" || run.Spoiler && !r.revealed
}

// entityCursors shows a hand over what a click acts on in r's text, laid
// out last, as over a button; the rest of the text keeps the text cursor.
// The areas take no events: the text's own area handles them.
func entityCursors(gtx layout.Context, r *messageRow) {
	for _, f := range r.text.fragments {
		if f.Index < 0 || f.Index >= len(r.runs) || !r.actsOn(r.runs[f.Index]) || f.Bounds.Empty() {
			continue
		}
		area := clip.Rect(f.Bounds).Push(gtx.Ops)
		pointer.CursorPointer.Add(gtx.Ops)
		area.Pop()
	}
}
