// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"sort"
	"time"

	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/styledtext"
)

// The text of a draft a bot streams types itself in, as Telegram for
// Android shows it (MultiLayoutTypingAnimator): a caret goes along the
// lines of the text, what is before it shows, the line it is on comes in
// under an edge that fades, and what is after it waits. It goes fast
// enough to reach the end of what came in typingCatchUp, and at least
// typingSpeed; what comes in later carries it on. The message the draft
// becomes takes the caret over and finishes the line.

const (
	typingSpeed   unit.Dp = 40
	typingCatchUp         = 1050 * time.Millisecond
	typingEdge    unit.Dp = 50
	// typingFadeIn is how long what is between two lines, a picture or
	// a table's frame, takes to show once the caret passed it.
	typingFadeIn = 200 * time.Millisecond
	// typingSteps are the strips the fading edge is drawn in.
	typingSteps = 10
)

// typing is how far a row's text has typed itself in.
type typing struct {
	// sender wrote it: a message of the sender's takes the draft's
	// caret over.
	sender int64
	// line is the line the caret is on, x how far into it, in pixels.
	line int
	x    float32
	// speed is in pixels a second; total, the width of the lines when
	// it was set, which tells that more text came in.
	speed float32
	total int
	last  time.Time
	// passed is the line the caret left last, at passedAt: what is under
	// it and over the caret's line fades in.
	passed   int
	passedAt time.Time
	// height is how high the text shows, as it grows to the caret's line.
	height heightTransition
	// row is the row the caret went along last, and lines its lines.
	row   *messageRow
	lines []typingLine
}

// typingLine is a line of a row's text, in its text area.
type typingLine struct {
	top, bottom, left, right int
}

// typingLines are the lines of fragments, top to bottom: fragments whose
// middles are in a line's height are of that line.
func typingLines(fragments []styledtext.Fragment) []typingLine {
	bounds := make([]image.Rectangle, 0, len(fragments))
	for _, f := range fragments {
		if !f.Bounds.Empty() {
			bounds = append(bounds, f.Bounds)
		}
	}
	sort.SliceStable(bounds, func(i, j int) bool {
		if bounds[i].Min.Y != bounds[j].Min.Y {
			return bounds[i].Min.Y < bounds[j].Min.Y
		}
		return bounds[i].Min.X < bounds[j].Min.X
	})
	var lines []typingLine
	for _, b := range bounds {
		mid := (b.Min.Y + b.Max.Y) / 2
		if n := len(lines); n > 0 && mid < lines[n-1].bottom {
			l := &lines[n-1]
			l.top, l.bottom = min(l.top, b.Min.Y), max(l.bottom, b.Max.Y)
			l.left, l.right = min(l.left, b.Min.X), max(l.right, b.Max.X)
			continue
		}
		lines = append(lines, typingLine{b.Min.Y, b.Max.Y, b.Min.X, b.Max.X})
	}
	return lines
}

// advance moves the caret on to now, along lines.
func (t *typing) advance(gtx layout.Context, lines []typingLine) {
	total := 0
	for _, l := range lines {
		total += l.right - l.left
	}
	if t.last.IsZero() || total != t.total {
		// What is left goes in typingCatchUp, not slower than typingSpeed.
		left := float32(0)
		for i := t.line; i < len(lines); i++ {
			left += float32(lines[i].right - lines[i].left)
			if i == t.line {
				left -= t.x
			}
		}
		t.speed = max(float32(gtx.Dp(typingSpeed)), left/float32(typingCatchUp.Seconds()))
		t.total = total
	}
	if t.last.IsZero() {
		t.last = gtx.Now
	}
	step := t.speed * float32(gtx.Now.Sub(t.last).Seconds())
	t.last = gtx.Now
	for step > 0 && t.line < len(lines) {
		left := float32(lines[t.line].right-lines[t.line].left) - t.x
		if step < left {
			t.x += step
			break
		}
		step -= max(left, 0)
		t.passed, t.passedAt = t.line, gtx.Now
		t.line, t.x = t.line+1, 0
	}
}

// follow moves the caret to the lines of a new row, as when the draft
// grows or is edited. As Telegram for Android keeps it (setBlocks), a
// caret at the end of the old text stays at the end of its last line, at
// that line's width, and a caret goes on from its line and its place in
// pixels whatever the new text is: words added to that line type
// themselves in, and of an answer that replaces "Analyzing..." the part as
// wide as that shows at once.
func (t *typing) follow(r *messageRow) {
	if n := len(t.lines); n > 0 && t.line >= n {
		last := t.lines[n-1]
		t.line, t.x = n-1, float32(last.right-last.left)
	}
	t.row = r
}

// typingStep moves on the caret of r, whose text area laid out is size
// big, and returns it with the lines of the text and how high the area
// shows: down to the caret's line, growing to it as the caret goes, so
// that the history, kept at its end, follows the text and not what has
// yet to show. It returns nil, and size's height, unless r is a draft or
// the message one became.
func (p *chatPage) typingStep(gtx layout.Context, r *messageRow, size image.Point, animate bool) (*typing, []typingLine, int) {
	animate = animate && wdk.AnimationsEnabled(gtx)
	t := p.typing[r.key.MessageID]
	if t == nil && r.streaming && animate {
		if p.typing == nil {
			p.typing = map[model.MessageID]*typing{}
		}
		t = &typing{sender: r.sender, passed: -1}
		p.typing[r.key.MessageID] = t
	}
	if t == nil || !animate {
		return nil, nil, size.Y
	}
	lines := typingLines(r.text.fragments)
	if r != t.row {
		t.follow(r)
	}
	t.advance(gtx, lines)
	t.lines = lines
	target := size.Y
	if t.line < len(lines) {
		target = lines[t.line].bottom
	}
	return t, lines, min(size.Y, t.height.Value(gtx, target, true))
}

// typed adds call, the text area of r size big, as far as t has typed it
// in; all of it when t is nil.
func (p *chatPage) typed(gtx layout.Context, r *messageRow, t *typing, lines []typingLine, call op.CallOp, size image.Point) {
	if t == nil {
		call.Add(gtx.Ops)
		return
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	caret := size.Y
	if t.line < len(lines) {
		caret = lines[t.line].top
	}
	// What is under the line the caret left fades in.
	shown := caret
	fade := float32(1)
	if t.passed >= 0 && t.passed < len(lines) && t.passed < t.line {
		if fade = float32(gtx.Now.Sub(t.passedAt)) / float32(typingFadeIn); fade < 1 {
			shown = min(caret, lines[t.passed].bottom)
		}
	}
	part := func(rect image.Rectangle, opacity float32) {
		if rect.Empty() || opacity <= 0 {
			return
		}
		defer clip.Rect(rect).Push(gtx.Ops).Pop()
		if opacity < 1 {
			defer paint.PushOpacity(gtx.Ops, opacity).Pop()
		}
		call.Add(gtx.Ops)
	}
	part(image.Rect(0, 0, size.X, shown), 1)
	part(image.Rect(0, shown, size.X, caret), fade)
	if t.line < len(lines) {
		// The caret's line shows up to an edge that fades, which starts
		// a fade's width before the line and ends past it.
		l := lines[t.line]
		width := float32(l.right - l.left)
		edge := float32(gtx.Dp(typingEdge))
		at := float32(l.left) - edge + (width+edge)*t.x/max(width, 1)
		part(image.Rect(0, l.top, int(at), l.bottom), 1)
		for i := range typingSteps {
			x0 := at + edge*float32(i)/typingSteps
			x1 := at + edge*float32(i+1)/typingSteps
			part(image.Rect(int(x0), l.top, int(x1), l.bottom), 1-(float32(i)+.5)/typingSteps)
		}
	}
	if t.line < len(lines) || fade < 1 || t.height.value < t.height.target {
		gtx.Execute(op.InvalidateCmd{})
	} else if !r.streaming {
		// The message has typed itself in.
		delete(p.typing, r.key.MessageID)
	}
}

// handOverTyping gives the caret of a draft that goes to the message of
// its sender that comes with messages, the history's next, as the store
// drops a draft for the message it becomes; the carets of other rows that
// go are dropped.
func (p *chatPage) handOverTyping(messages []model.Message) {
	if len(p.typing) == 0 {
		return
	}
	was := make(map[model.MessageID]bool, len(p.messages))
	for _, m := range p.messages {
		was[m.Key.MessageID] = true
	}
	alive := make(map[model.MessageID]bool, len(messages))
	for _, m := range messages {
		alive[m.Key.MessageID] = true
	}
	for id, t := range p.typing {
		if alive[id] {
			continue
		}
		delete(p.typing, id)
		if id >= 0 {
			continue
		}
		for i := len(messages) - 1; i >= 0; i-- {
			m := messages[i]
			if !was[m.Key.MessageID] && !m.Streaming && m.SenderID == t.sender && p.typing[m.Key.MessageID] == nil {
				// It goes on where it is; its speed is set again.
				t.last, t.total = time.Time{}, 0
				p.typing[m.Key.MessageID] = t
				break
			}
		}
	}
}
