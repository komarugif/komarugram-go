// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"maps"
	"sort"
	"time"

	"komarugram/internal/messenger/model"

	"gioui.org/f32"
	"gioui.org/gesture"
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
	// selected contains rows with a selection. An entry in parts limits
	// that row's selection to the given album members.
	selected    map[model.MessageID]bool
	parts       map[model.MessageID]map[model.MessageID]bool
	before      map[model.MessageID]bool
	beforeParts map[model.MessageID]map[model.MessageID]bool
	dragging    bool
	pointer     pointer.ID
	anchor      model.MessageID
	selecting   bool
	position    f32.Point
	scrollAt    time.Time
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

func (s *messageSelection) setRow(id model.MessageID, selected bool) {
	delete(s.parts, id)
	if selected {
		if s.selected == nil {
			s.selected = make(map[model.MessageID]bool)
		}
		s.selected[id] = true
	} else {
		delete(s.selected, id)
	}
}

func (s *messageSelection) setAlbumParts(m model.Message, parts map[model.MessageID]bool) {
	id := m.Key.MessageID
	s.setRow(id, len(parts) > 0)
	if len(parts) > 0 && len(parts) < len(messageParts(m)) {
		if s.parts == nil {
			s.parts = make(map[model.MessageID]map[model.MessageID]bool)
		}
		s.parts[id] = parts
	}
}

func (s *messageSelection) toggleAlbumPart(m model.Message, id model.MessageID) {
	row := m.Key.MessageID
	if id == 0 {
		s.setRow(row, !s.selected[row] || s.parts[row] != nil)
		return
	}
	parts := maps.Clone(s.parts[row])
	if parts == nil {
		parts = make(map[model.MessageID]bool)
		if s.selected[row] {
			for _, member := range m.Attachments {
				parts[member.Key.MessageID] = true
			}
		}
	}
	if parts[id] {
		delete(parts, id)
	} else {
		parts[id] = true
	}
	s.setAlbumParts(m, parts)
}

func (p *chatPage) pruneSelection(alive map[model.MessageID]bool) {
	s := &p.selection
	if len(s.parts) > 0 {
		// The first member may have changed, or only one may remain.
		picked := make(map[model.MessageID]bool)
		for row, parts := range s.parts {
			maps.Copy(picked, parts)
			delete(s.selected, row)
		}
		clear(s.parts)
		for _, m := range p.messages {
			if s.selected[m.Key.MessageID] {
				continue
			}
			var parts map[model.MessageID]bool
			for _, member := range messageParts(m) {
				if id := member.Key.MessageID; picked[id] {
					if parts == nil {
						parts = make(map[model.MessageID]bool)
					}
					parts[id] = true
				}
			}
			if len(parts) > 0 {
				s.setAlbumParts(m, parts)
			}
		}
	}
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
	s.parts = maps.Clone(s.beforeParts)
	lo, hi := min(s.anchor, end), max(s.anchor, end)
	for _, m := range p.messages {
		id := m.Key.MessageID
		if id < lo || id > hi || m.Kind == model.MessageService {
			continue
		}
		s.setRow(id, s.selecting)
	}
}
func (p *chatPage) rowAt(y float32) int {
	if p.heights == nil || len(p.messages) == 0 {
		return -1
	}
	first := p.list.Position.First
	if first < 0 || first >= len(p.above)+len(p.messages) {
		return -1
	}
	absolute := p.heights.Prefix(first) + int64(p.list.Position.Offset) + int64(y)
	row, _ := p.heights.Find(absolute)
	index := row - len(p.above)
	if index < 0 || index >= len(p.messages) {
		// A skeleton, of no message loaded.
		return -1
	}
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
			if i < 0 || p.messages[i].Kind == model.MessageService || p.messages[i].Local() {
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
				s.selected, s.parts = s.before, s.beforeParts
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
	s.beforeParts = maps.Clone(s.parts)
	s.selecting = !s.selected[s.anchor] || s.parts[s.anchor] != nil
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
	above := len(p.above)
	first := p.list.Position.First - above
	if first >= len(p.messages) || p.heights == nil {
		return
	}
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	top := -p.list.Position.Offset
	if first < 0 {
		// Under the skeletons of rows let go of.
		top += int(p.heights.Prefix(above) - p.heights.Prefix(p.list.Position.First))
		first = 0
	}
	for i := first; i < len(p.messages) && top < gtx.Constraints.Max.Y; i++ {
		m := p.messages[i]
		height := int(p.heights.Prefix(above+i+1) - p.heights.Prefix(above+i))
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
			if parts := p.selection.parts[id]; parts != nil {
				n += len(parts)
			} else {
				n += max(1, len(p.messages[i].Attachments))
			}
		}
	}

	return n
}

// updateAttachmentSelection handles clicks in the media's own coordinates.
// The surrounding gutters keep their usual whole-row click and drag input.
func (p *chatPage) updateAttachmentSelection(gtx layout.Context, r *messageRow, m model.Message) bool {
	if !gtx.Enabled() || p.snapshotting || m.Local() || m.Kind == model.MessageService || len(p.selection.selected) == 0 {
		return false
	}
	memberAt := func(pos image.Point) model.MessageID {
		for i, tile := range r.tiles {
			if i < len(m.Attachments) && pos.In(tile) {
				return m.Attachments[i].Key.MessageID
			}
		}
		return 0
	}
	for {
		e, ok := r.attachmentPick.Update(gtx.Source)
		if !ok {
			break
		}
		switch e.Kind {
		case gesture.KindPress:
			r.attachmentPressed = memberAt(e.Position)
		case gesture.KindClick:
			if id := memberAt(e.Position); id == r.attachmentPressed {
				p.selection.toggleAlbumPart(m, id)
				gtx.Execute(key.FocusCmd{Tag: &p.keyboard})
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
	return len(p.selection.selected) > 0
}

// attachmentSelectionArea takes clicks over the media and leaves an
// album's right-click observers above it for the message menu.
func attachmentSelectionArea(gtx layout.Context, r *messageRow, m model.Message, size image.Point) {
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	pointer.CursorPointer.Add(gtx.Ops)
	r.attachmentPick.Add(gtx.Ops)
	for i, tile := range r.tiles {
		child := r.album[m.Attachments[i].Key.MessageID]
		at := op.Offset(tile.Min).Push(gtx.Ops)
		partPressArea(gtx, child, tile.Size())
		at.Pop()
	}
}

func (s *messageSelection) markAlbumPart(gtx layout.Context, album, member model.MessageID, size image.Point) {
	d := min(gtx.Dp(24), size.X-gtx.Dp(12), size.Y-gtx.Dp(12))
	if d <= 0 {
		return
	}
	selected := s.selected[album]
	if parts := s.parts[album]; parts != nil {
		selected = parts[member]
	}
	at := image.Pt(size.X-d-gtx.Dp(6), gtx.Dp(6))
	offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, scheme(gtx).Surface.Color, image.Pt(d, d), d/2)
		return selectionMark(gtx, d, selected)
	})
}
