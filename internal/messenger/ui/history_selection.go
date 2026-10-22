// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"sort"
	"time"

	"komarugram/internal/messenger/model"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// Selection uses stable message IDs, not virtualized row positions. A drag
// applies a range to a snapshot, so reversing direction restores crossed rows.
type messageSelection struct {
	selected  map[model.MessageID]bool
	before    map[model.MessageID]bool
	dragging  bool
	pointer   pointer.ID
	anchor    model.MessageID
	selecting bool
	position  f32.Point
	scrollAt  time.Time
	// pending is a press beside a message with nothing selected: it
	// starts selecting only once the mouse moves away, from origin.
	pending bool
	origin  f32.Point
	// clicked and clickedAt are the message and the time of the last
	// press beside one, to tell a double click, which replies to it.
	clicked   model.MessageID
	clickedAt time.Duration
}

// gutterDoubleClick is the longest time between the presses of a double
// click beside a message, the same as gesture.Click's on the bubble.
const gutterDoubleClick = 200 * time.Millisecond

// holdsKeyboard reports whether the history keeps the keyboard: for
// messages or text selected, or text being selected, to copy or extend.
func (p *chatPage) holdsKeyboard() bool {
	if len(p.selection.selected) > 0 || p.selection.dragging {
		return true
	}
	t := p.activeText
	return t != nil && (t.text.pressed || t.text.anchor != t.text.caret)
}

func (p *chatPage) clearSelection() {
	p.selection = messageSelection{}
}
func (p *chatPage) pruneSelection(alive map[model.MessageID]bool) {
	for id := range p.selection.selected {
		if !alive[id] {
			delete(p.selection.selected, id)
		}
	}
	if p.selection.dragging && !alive[p.selection.anchor] {
		p.selection.dragging = false
	}
	if p.activeText != nil {
		found := false
		for _, r := range p.rows {
			if r == p.activeText {
				found = true
				break
			}
			for _, child := range r.album {
				if child == p.activeText {
					found = true
					break
				}
			}
		}
		if !found {
			p.activeText = nil
		}
	}
}
func (p *chatPage) rangeSelection(end model.MessageID) {
	s := &p.selection
	if end < 0 {
		// A draft a bot streams is no message to select, nor its range.
		return
	}
	s.selected = make(map[model.MessageID]bool, len(s.before))
	for id, selected := range s.before {
		if selected {
			s.selected[id] = true
		}
	}
	lo, hi := min(s.anchor, end), max(s.anchor, end)
	for _, m := range p.messages {
		id := m.Key.MessageID
		if id < lo || id > hi || m.Kind == model.MessageService {
			continue
		}
		if s.selecting {
			s.selected[id] = true
		} else {
			delete(s.selected, id)
		}
	}
}
func (p *chatPage) rowAt(y float32) int {
	if p.heights == nil || len(p.messages) == 0 {
		return -1
	}
	first := p.list.Position.First
	if first < 0 || first >= len(p.messages) {
		return -1
	}
	absolute := p.heights.Prefix(first) + int64(p.list.Position.Offset) + int64(y)
	index, _ := p.heights.Find(absolute)
	return index
}
func (p *chatPage) selectionEvents(gtx layout.Context) {
	s := &p.selection
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: s, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e := ev.(pointer.Event)
		switch e.Kind {
		case pointer.Press:
			if s.dragging || (e.Source == pointer.Mouse && e.Buttons != pointer.ButtonPrimary) {
				continue
			}
			i := p.rowAt(e.Position.Y)
			if i < 0 || p.messages[i].Kind == model.MessageService || p.messages[i].Streaming {
				continue
			}
			p.activeText = nil
			id := p.messages[i].Key.MessageID
			if len(s.selected) == 0 {
				// As in Telegram Desktop: with nothing selected, a click
				// beside a message does nothing, a double click replies to
				// it, and a drag of the mouse selects.
				if s.clicked == id && e.Time-s.clickedAt < gutterDoubleClick {
					s.clicked, s.pending = 0, false
					if m := p.messages[i]; p.canReply(m) {
						p.composer.replyTo(gtx, p.chat, m)
					}
					continue
				}
				s.clicked, s.clickedAt = id, e.Time
				// A touch drags the history instead.
				s.pending = e.Source == pointer.Mouse
				s.anchor, s.pointer, s.origin = id, e.PointerID, e.Position
				continue
			}
			s.anchor = id
			p.startSelection(gtx, e)
		case pointer.Drag, pointer.Release:
			if s.pending && e.PointerID == s.pointer {
				if e.Kind == pointer.Release {
					s.pending = false
					continue
				}
				i := p.rowAt(e.Position.Y)
				d := e.Position.Sub(s.origin)
				moved := max(d.X, -d.X)+max(d.Y, -d.Y) >= float32(gtx.Dp(10))
				if !moved && (i < 0 || p.messages[i].Key.MessageID == s.anchor) {
					continue
				}
				s.pending, s.clicked = false, 0
				p.startSelection(gtx, e)
			}
			if !s.dragging || e.PointerID != s.pointer {
				continue
			}
			s.position = e.Position
			if i := p.rowAt(e.Position.Y); i >= 0 {
				p.rangeSelection(p.messages[i].Key.MessageID)
			}
			if e.Kind == pointer.Release {
				s.dragging = false
			}
		case pointer.Cancel:
			s.pending = false
			if s.dragging {
				s.selected = s.before
				s.dragging = false
			}
		}
	}
	if s.dragging {
		edge := float32(gtx.Dp(28))
		speed := float32(0)
		if s.position.Y < edge {
			speed = -1
		} else if s.position.Y > float32(gtx.Constraints.Max.Y)-edge-float32(gtx.Dp(80)) {
			speed = 1
		}
		if speed != 0 {
			dt := min(float32(gtx.Now.Sub(s.scrollAt).Seconds()), .05)
			p.list.Position.Offset += int(speed * float32(gtx.Dp(420)) * dt)
			p.list.Position.BeforeEnd = true
			if i := p.rowAt(s.position.Y); i >= 0 {
				p.rangeSelection(p.messages[i].Key.MessageID)
			}
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
			if p.trace != nil {
				p.trace.Recorder.Count(p.trace.Window, "history.invalidate.selection-scroll")
			}
		}
		s.scrollAt = gtx.Now
	}
}

// startSelection selects or unselects the message s.anchor, as it is not
// or is selected, and goes on doing so to the messages the pointer of e
// drags over.
func (p *chatPage) startSelection(gtx layout.Context, e pointer.Event) {
	s := &p.selection
	s.before = make(map[model.MessageID]bool, len(s.selected))
	for id, v := range s.selected {
		s.before[id] = v
	}
	s.selecting = !s.selected[s.anchor]
	s.dragging, s.pointer, s.position, s.scrollAt = true, e.PointerID, e.Position, gtx.Now
	p.activeText = nil
	p.rangeSelection(s.anchor)
	gtx.Execute(pointer.GrabCmd{Tag: s, ID: e.PointerID})
	gtx.Execute(key.FocusCmd{Tag: &p.keyboard})
}

// Register only the free gutters alongside bubbles. They select by a drag,
// or by a click once a message is selected, and reply by a double click,
// but keep the ordinary cursor, as in Telegram Desktop. Text, media, dates
// and the scrollbar keep their own input handling.
func (p *chatPage) selectionAreas(gtx layout.Context) {
	first := p.list.Position.First
	if first < 0 || first >= len(p.messages) || p.heights == nil {
		return
	}
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	top := -p.list.Position.Offset
	for i := first; i < len(p.messages) && top < gtx.Constraints.Max.Y; i++ {
		m := p.messages[i]
		height := int(p.heights.Prefix(i+1) - p.heights.Prefix(i))
		if r := p.rows[m.Key.MessageID]; r != nil && m.Kind != model.MessageService {
			left := r.bodyLeft
			right := left + r.bodySize.X
			for _, rect := range []image.Rectangle{
				image.Rect(0, top+r.bodyTop, left, top+height),
				image.Rect(right, top+r.bodyTop, max(right, gtx.Constraints.Max.X-gtx.Dp(12)), top+height),
			} {
				if rect.Empty() {
					continue
				}
				pass := pointer.PassOp{}.Push(gtx.Ops)
				area := clip.Rect(rect).Push(gtx.Ops)
				event.Op(gtx.Ops, &p.selection)
				area.Pop()
				pass.Pop()
			}
		}
		top += height
	}
}

func (p *chatPage) selectionCount() int {
	if len(p.selection.selected) == 0 {
		return 0
	}
	n := 0
	for id := range p.selection.selected {
		i := sort.Search(len(p.messages), func(i int) bool { return p.messages[i].Key.MessageID >= id })
		if i < len(p.messages) && p.messages[i].Key.MessageID == id {
			n += max(1, len(p.messages[i].Attachments))
		}
	}

	return n
}
