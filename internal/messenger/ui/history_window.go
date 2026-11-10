// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"sort"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
)

// The store keeps a window of a chat's history loaded around the view, and
// lets go of the messages farthest from it as pages come at the other end
// (tgstore's history_window.go). Their rows keep their room: those above
// the first loaded message are rows of the list before it, and those below
// the last join the reserve; both are drawn as skeletons, as high as they
// were laid out, until their pages come again. The scrollbar stands where
// it was, and the view stays at its row.

// skeletonAt is the skeleton of row of the list, above or below the
// messages loaded.
func (p *chatPage) skeletonAt(row int) skeletonRow {
	if row < len(p.above) {
		return p.above[row]
	}
	return p.below[row-len(p.above)-len(p.messages)]
}

// edgeRow tells whether loaded message i is at an end of what is loaded
// with more past it: the messages next to it, by which its row is laid
// out, are not at hand.
func (p *chatPage) edgeRow(i int) bool {
	return i == 0 && p.olderAbove || i == len(p.messages)-1 && p.newerNow
}

// keepRoom keeps the room of the rows of the loaded messages the store let
// go of, as messages, with what the history has now, takes their place:
// above the first of them, and below the last, in the reserve. Rows above
// that are loaded again go; messages that share none with those loaded
// before, as a jump brings, keep none. Messages are told apart by their
// ids, not by the rows' keys: an album cut by a page is keyed by another
// part once the page before it comes. It runs as the history is built
// again, before it takes messages.
func (p *chatPage) keepRoom(messages []model.Message, env model.RenderEnvironment) {
	old := p.messages
	p.room = nil
	if len(messages) == 0 || len(old) == 0 {
		p.above = nil
		return
	}
	if !overlap(old, messages) {
		// A leap into the room kept moves the rows between; a jump
		// keeps none.
		if !p.moveRoom(messages, env) {
			p.above = nil
		}
		return
	}
	first, last := messages[0].Key.MessageID, rowEnd(messages[len(messages)-1])
	var above []skeletonRow
	if p.olderAbove {
		for _, s := range p.above {
			if s.id < first {
				above = append(above, s)
			}
		}
		for i, m := range old {
			if rowEnd(m) >= first {
				break
			}
			above = append(above, p.roomOf(i))
		}
	}
	var below []skeletonRow
	if p.newerNow && env == p.env {
		for i, m := range old {
			if m.Key.MessageID > last && !m.Local() {
				below = append(below, p.roomOf(i))
			}
		}
	}
	if len(p.above) > 0 {
		p.room = make(map[model.MessageID]int, len(p.above))
		for _, s := range p.above {
			p.room[s.id] = s.height
		}
	}
	p.above = above
	p.keepShapes(above)
	p.keepShapes(below)
	p.wantShapes(above)
	p.wantShapes(below)
	if len(below) > 0 {
		r := &p.reserve
		if r.chat != p.chat || r.env != env {
			*r = reserve{chat: p.chat, env: env}
		}
		r.merge(below)
		r.room = true
	}
}

// rowEnd is the newest message of row m: its own, or an album's last part.
func rowEnd(m model.Message) model.MessageID {
	end := m.Key.MessageID
	for _, a := range m.Attachments {
		end = max(end, a.Key.MessageID)
	}
	return end
}

// overlap tells whether rows a and b have a message in common, of
// themselves or of their albums.
func overlap(a, b []model.Message) bool {
	if len(a) == 0 || len(b) == 0 || rowEnd(a[len(a)-1]) < b[0].Key.MessageID || rowEnd(b[len(b)-1]) < a[0].Key.MessageID {
		return false
	}
	ids := make(map[model.MessageID]bool, len(a))
	for _, m := range a {
		ids[m.Key.MessageID] = true
		for _, part := range m.Attachments {
			ids[part.Key.MessageID] = true
		}
	}
	for _, m := range b {
		if ids[m.Key.MessageID] {
			return true
		}
		for _, part := range m.Attachments {
			if ids[part.Key.MessageID] {
				return true
			}
		}
	}
	return false
}

// roomOf is the skeleton of loaded message i's row, as high as the list
// has it: laid out, or as it was guessed.
func (p *chatPage) roomOf(i int) skeletonRow {
	m := p.messages[i]
	s := skeletonRow{id: m.Key.MessageID, outgoing: m.Outgoing, service: m.ServicePill()}
	row := len(p.above) + i
	if p.heights != nil && row < p.heights.Len() {
		s.height = int(p.heights.Prefix(row+1) - p.heights.Prefix(row))
		if row == p.heights.Len()-1 {
			s.height -= p.tail
		}
	}
	if l, ok := p.measures[m.Key.MessageID]; ok && l.ContentRevision == m.ContentRevision {
		s.height = l.HeightPx
	}
	s.height = max(0, s.height)
	if r := p.rows[m.Key.MessageID]; r != nil && r.bodySize.X > 0 {
		if p.senderAvatar(m) != 0 && p.avatar != nil {
			// The avatar's 34 dp and the gap of 8 after it.
			s.slot = (34 + 8) * p.env.ScaleMilli / 1000
		}
		s.bubble = image.Pt(max(0, r.bodySize.X-s.slot), r.bodySize.Y)
		s.shape = shapeLaidOut
	} else if k, ok := p.shapes[m.Key.MessageID]; ok {
		// Loaded and let go of before a frame laid it out: as its
		// skeleton was.
		s.bubble, s.slot, s.shape = k.bubble, k.slot, k.shape
	}
	if k, ok := p.shapes[m.Key.MessageID]; ok {
		s.profile = k.profile
	}
	return s
}

// keepShapes keeps the bubbles of rows, skeletons the history has, for
// roomOf.
func (p *chatPage) keepShapes(rows []skeletonRow) {
	for _, s := range rows {
		if s.bubble.X <= 0 && s.profile == nil {
			continue
		}
		if p.shapes == nil {
			p.shapes = map[model.MessageID]skeletonRow{}
		}
		k := p.shapes[s.id]
		if s.bubble.X > 0 {
			k.bubble, k.slot, k.shape = s.bubble, s.slot, s.shape
		}
		if s.profile != nil {
			k.profile = s.profile
		}
		p.shapes[s.id] = k
	}
}

// merge puts rows in the reserve, in the place of the rows of their
// messages it has: one pass over both, sorted by message, a row of no shape
// keeping the one the reserve's had.
func (r *reserve) merge(rows []skeletonRow) {
	if len(rows) == 0 {
		return
	}
	if !sort.SliceIsSorted(rows, func(i, j int) bool { return rows[i].id < rows[j].id }) {
		sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	}
	if r.heights == nil {
		r.heights = make(map[model.MessageID]int, len(rows))
	}
	for _, s := range rows {
		r.heights[s.id] = s.height
	}
	if n := len(r.rows); n == 0 || r.rows[n-1].id < rows[0].id {
		// After all it has, as rows let go of below the last loaded come
		// before the reserve worked out after them.
		r.rows = append(r.rows, rows...)
		return
	}
	out := make([]skeletonRow, 0, len(r.rows)+len(rows))
	i, k := 0, 0
	for i < len(r.rows) || k < len(rows) {
		switch {
		case k == len(rows) || i < len(r.rows) && r.rows[i].id < rows[k].id:
			out = append(out, r.rows[i])
			i++
		case i == len(r.rows) || rows[k].id < r.rows[i].id:
			out = append(out, rows[k])
			k++
		default:
			s, old := rows[k], r.rows[i]
			if s.bubble.X <= 0 {
				s.bubble, s.slot, s.shape = old.bubble, old.slot, old.shape
			}
			if s.profile == nil {
				s.profile = old.profile
			}
			out = append(out, s)
			i++
			k++
		}
	}
	r.rows = out
}

// shapeFrom is where a skeleton's bubble came from.
type shapeFrom uint8

const (
	// shapeNone is no bubble known: the skeleton draws one of two thirds
	// of the width.
	shapeNone shapeFrom = iota
	// shapeProfile is the bubble the row's profile told, in the reserve.
	shapeProfile
	// shapeMeasured is the bubble the measurer laid out off screen.
	shapeMeasured
	// shapeLaidOut is the bubble a frame laid out, of a row let go of.
	shapeLaidOut
)

func (s shapeFrom) String() string {
	return [...]string{"none", "profile", "measured", "laid out"}[s]
}

// seenSkeleton keeps skeleton s, drawn in view, for skeletonReplaced to
// tell the journal how it matched the row that took its place.
func (p *chatPage) seenSkeleton(s skeletonRow) {
	if !historylog.On() {
		return
	}
	if p.seenSkeletons == nil {
		p.seenSkeletons = map[model.MessageID]skeletonRow{}
	}
	p.seenSkeletons[s.id] = s
}

// skeletonReplaced tells the journal of a row laid out height high where
// its skeleton was drawn before, when they differ by more than a few
// pixels: how high each is, how large their bubbles, and where the
// skeleton's came from.
func (p *chatPage) skeletonReplaced(m model.Message, height int) {
	s, ok := p.seenSkeletons[m.Key.MessageID]
	if !ok {
		return
	}
	delete(p.seenSkeletons, m.Key.MessageID)
	r := p.rows[m.Key.MessageID]
	if r == nil {
		return
	}
	slot := 0
	if p.senderAvatar(m) != 0 && p.avatar != nil {
		slot = (34 + 8) * p.env.ScaleMilli / 1000
	}
	bubble := image.Pt(max(0, r.bodySize.X-slot), r.bodySize.Y)
	off := func(a, b int) bool { return a-b > 2 || b-a > 2 }
	if !off(s.height, height) && !off(s.bubble.X, bubble.X) && !off(s.bubble.Y, bubble.Y) {
		return
	}
	historylog.Printf("skeleton: chat %d message %d (kind %d, media %t, album %t): skeleton %d px, bubble %dx%d from %s; laid out %d px, bubble %dx%d", p.chat, m.Key.MessageID, m.Kind, m.Media != nil, len(m.Attachments) > 1, s.height, s.bubble.X, s.bubble.Y, s.shape, height, bubble.X, bubble.Y)
}

// shapesLimit is the most rows a read of profiles for shapes brings.
const shapesLimit = 2000

// shapesResult is the profiles the cache keeps of rows, of a chat in an
// environment, by message.
type shapesResult struct {
	chat     int64
	env      model.RenderEnvironment
	profiles map[model.MessageID][]byte
}

// wantShapes notes the rows of no bubble known, for askShapes.
func (p *chatPage) wantShapes(rows []skeletonRow) {
	for _, s := range rows {
		if s.profile != nil {
			continue
		}
		if p.shapesWanted == nil {
			p.shapesWanted = map[model.MessageID]bool{}
		}
		p.shapesWanted[s.id] = true
	}
}

// askShapes reads off the frame the profiles the cache keeps of the rows
// let go of before a frame laid them out, the oldest first, for the
// bubbles of their skeletons: as the reserve's are told.
func (p *chatPage) askShapes() {
	if p.shapesAsking || len(p.shapesWanted) == 0 || p.chat == 0 {
		return
	}
	source, ok := p.source.(model.RowShapes)
	if !ok {
		p.shapesWanted = nil
		return
	}
	low, high := model.MessageID(0), model.MessageID(0)
	for id := range p.shapesWanted {
		if low == 0 || id < low {
			low = id
		}
		high = max(high, id)
	}
	limit := min(shapesLimit, int(high-low)+1)
	for id := range p.shapesWanted {
		if id < low+model.MessageID(limit) {
			delete(p.shapesWanted, id)
		}
	}
	if p.shapesDone == nil {
		p.shapesDone = make(chan shapesResult, 1)
	}
	p.shapesAsking = true
	done, chat, env, invalidate := p.shapesDone, p.chat, p.env, p.invalidate
	go func() {
		defer crash.Recover("history shapes", func(*crash.Panic) {
			done <- shapesResult{chat: chat, env: env}
			invalidate()
		})
		// The profiles alone: they are decoded for the skeletons drawn.
		profiles := source.ProfilesBetween(chat, env, rowProfileVersion, low, low+model.MessageID(limit)-1)
		done <- shapesResult{chat: chat, env: env, profiles: profiles}
		invalidate()
	}()
}

// takeShapes gives the skeletons the profiles of their rows read from the
// cache, by which they are drawn (skeleton_shape.go), when a read came for
// the chat and environment the history is in.
func (p *chatPage) takeShapes() {
	var r shapesResult
	select {
	case r = <-p.shapesDone:
	default:
		return
	}
	p.shapesAsking = false
	if r.chat != p.chat || r.env != p.env || len(r.profiles) == 0 {
		return
	}
	give := func(s *skeletonRow) {
		if data, ok := r.profiles[s.id]; ok && s.profile == nil {
			s.profile = data
		}
	}
	for i := range p.above {
		give(&p.above[i])
	}
	// The rows below are the reserve's own.
	for i := range p.reserve.rows {
		give(&p.reserve.rows[i])
	}
	if p.shapes == nil {
		p.shapes = map[model.MessageID]skeletonRow{}
	}
	for id, data := range r.profiles {
		k := p.shapes[id]
		if k.profile == nil {
			k.profile = data
			p.shapes[id] = k
		}
	}
	p.invalidate()
}

// leapRows is how far in rows the view goes past what is loaded, into the
// skeletons, before the history leaps there rather than page to it.
const leapRows = 50

// leap has the store load the history around the row of the view at once,
// when the view is leapRows or more into the skeletons above or below
// what is loaded, as a dragged scrollbar takes it; it tells whether it
// asked.
func (p *chatPage) leap(history model.History) bool {
	if !p.restored || history.LoadingOlder || history.LoadingNewer || history.Err != nil || len(p.messages) == 0 {
		return false
	}
	w, ok := p.source.(model.HistoryWindow)
	if !ok {
		return false
	}
	first := p.list.Position.First
	above, loaded := len(p.above), len(p.messages)
	var target model.MessageID
	switch {
	case first < above && above-first >= leapRows:
		target = p.above[first].id
	case first >= above+loaded && first-above-loaded < len(p.below) && first-above-loaded+1 >= leapRows:
		target = p.below[first-above-loaded].id
	default:
		return false
	}
	if !w.LoadAround(p.chat, target) {
		return false
	}
	p.leaps++
	return true
}

// moveRoom keeps the room of the rows as the history leapt from the
// messages loaded to messages, which share none with them, within the
// rows whose room is kept: down into the reserve, the rows loaded and
// those between go above; up into the rows above, they go below. It tells
// whether messages were within that room.
func (p *chatPage) moveRoom(messages []model.Message, env model.RenderEnvironment) bool {
	old := p.messages
	if env != p.env {
		return false
	}
	first, last := messages[0].Key.MessageID, rowEnd(messages[len(messages)-1])
	oldFirst, oldLast := old[0].Key.MessageID, rowEnd(old[len(old)-1])
	r := &p.reserve
	switch {
	case first > oldLast:
		// Down, into the reserve: it must reach the new first.
		if n := len(r.rows); !p.olderAbove || r.chat != p.chat || r.env != env || n == 0 || r.rows[n-1].id < first {
			return false
		}
		// Grown in place: p.above keeps its length while roomOf reads the
		// rows by it.
		above := p.above
		for i, m := range old {
			if !m.Local() {
				above = append(above, p.roomOf(i))
			}
		}
		for _, s := range r.rows {
			if s.id > oldLast && s.id < first {
				above = append(above, s)
			}
		}
		p.above = above
		p.keepShapes(above)
		p.wantShapes(above)
		return true
	case last < oldFirst:
		// Up, into the rows above: they must reach the new first.
		if !p.olderAbove || len(p.above) == 0 || p.above[0].id > first {
			return false
		}
		var above, below []skeletonRow
		for _, s := range p.above {
			switch {
			case s.id < first:
				above = append(above, s)
			case s.id > last:
				below = append(below, s)
			}
		}
		for i, m := range old {
			if !m.Local() {
				below = append(below, p.roomOf(i))
			}
		}
		p.room = make(map[model.MessageID]int, len(p.above))
		for _, s := range p.above {
			p.room[s.id] = s.height
		}
		p.above = above
		if r.chat != p.chat || r.env != env {
			*r = reserve{chat: p.chat, env: env}
		}
		r.merge(below)
		r.room = true
		p.keepShapes(below)
		p.wantShapes(below)
		return true
	}
	return false
}
