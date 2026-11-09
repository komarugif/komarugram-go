// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
)

// A row's height is known once a frame laid it out, and a scroll fast
// enough goes past rows no frame lays out: the reserve ends before the
// first of them (history_reserve.go). The measurer lays such rows out off
// screen, in the frames in which the list stands still: the loaded rows
// first, nearest the view first, then the rows of the run the cache keeps
// past the reserve's end, which it reads from the cache a run at a time,
// and the reserve grows by them. Their media are not loaded for it, nor
// the messages their replies name looked up on Telegram: the cache has
// what it has of them, and a row whose reply names one it lacks is
// reserved as high as it was laid out, but its height is not kept.

const (
	// measureBudget is how long a frame lays rows out off screen: one row
	// at least.
	measureBudget = 4 * time.Millisecond
	// measureRun is how many messages a read of the cache brings the
	// measurer past the reserve's end.
	measureRun = 64
	// measureStill is how many frames in a row the list stands where it is
	// before rows are laid out off screen.
	measureStill = 2
)

// measurer lays out off screen the rows of a history no frame laid out.
type measurer struct {
	// gtx is a row's context as the list gave it in the last frame, which
	// rows off screen are laid out in; ops, the operations they record.
	gtx   layout.Context
	known bool
	ops   op.Ops
	// at is where the list stood in the last frame, and still how many
	// frames in a row it stood there.
	at    layout.Position
	still int
	// tried are the loaded rows laid out off screen, at their revisions:
	// each once. loadedDone is set once none is left, until the history is
	// built again.
	tried      map[model.MessageID]uint64
	loadedDone bool
	// targets are the messages replies name, read from the cache for rows
	// laid out off screen, and wanted those to read; unsure are the loaded
	// rows laid out without the message their reply names, by the message,
	// to lay out again once it is read.
	targets map[model.MessageID]measuredTarget
	wanted  map[model.MessageID]bool
	unsure  map[model.MessageID]model.MessageID
	// missing is the message a row laid out off screen names that is not
	// at hand; 0 for none.
	missing model.MessageID
	// gap is the run the cache keeps past the reserve's end, being
	// measured: its first is the reserve's last row, or the last loaded
	// one; joins and days are of its rows, next the index of the next to
	// lay out, and laid the skeletons of those laid out. whole is whether
	// the cache keeps the history whole past its last; the last row
	// otherwise waits for the run after it, which tells how it joins.
	gap   []model.Message
	joins []bubbleJoin
	days  []bool
	next  int
	laid  []skeletonRow
	whole bool
	// reading is set while a read of the cache is on its way, and done
	// brings it; growing while the reserve's own read after the gap is.
	reading, growing bool
	done             chan measureRead
	// offScreen are the heights rows laid out off screen had, for the
	// journal to tell of a frame that lays one out otherwise.
	offScreen map[model.MessageID]int
	// laidOut counts the rows laid out off screen, and took how long, for
	// the journal.
	laidOut int
	took    time.Duration
}

// measuredTarget is a message a reply names, as the cache keeps it; known
// is false when it keeps none.
type measuredTarget struct {
	msg   model.Message
	state model.LookupState
	known bool
}

// measureRead is a read of the cache for the measurer: of a chat in an
// environment, the run from message from on, and the messages the replies
// in it, or the replies asked, name.
type measureRead struct {
	chat    int64
	env     model.RenderEnvironment
	from    model.MessageID
	run     bool
	msgs    []model.Message
	targets map[model.MessageID]measuredTarget
}

// resetMeasurer forgets what the measurer did, as for another chat.
func (p *chatPage) resetMeasurer() {
	m := &p.measurer
	m.tried, m.targets, m.wanted, m.unsure, m.offScreen = nil, nil, nil, nil, nil
	m.dropGap()
	m.still, m.known, m.loadedDone = 0, false, false
	m.reading, m.growing = false, false
	// A read on its way is of the chat left: a new channel takes the next.
	m.done = nil
}

// dropGap forgets the run being measured.
func (m *measurer) dropGap() {
	m.gap, m.joins, m.days, m.next, m.laid, m.whole = nil, nil, nil, 0, nil, false
}

// rowContext keeps gtx, a row's context as the list gives it, for rows
// laid out off screen.
func (p *chatPage) rowContext(gtx layout.Context) {
	if !p.measurer.known {
		p.measurer.gtx, p.measurer.known = gtx, true
	}
}

// laidOutOnScreen tells the journal of a row a frame laid out height
// high, where it was laid out off screen at another height.
func (p *chatPage) laidOutOnScreen(id model.MessageID, height int) {
	m := &p.measurer
	if len(m.offScreen) == 0 {
		return
	}
	h, ok := m.offScreen[id]
	if !ok {
		return
	}
	delete(m.offScreen, id)
	if h != height && historylog.On() {
		historylog.Printf("measure: chat %d message %d laid out %d px high, off screen %d", p.chat, id, height, h)
	}
}

// measureOffScreen lays out rows no frame laid out, when the list stood
// still for frames: within measureBudget, and has a frame drawn again
// while some are left. It runs in the frame, after the list.
func (p *chatPage) measureOffScreen(gtx layout.Context, l localization.Catalog) {
	m := &p.measurer
	defer func() { m.known = false }()
	p.takeMeasureRead()
	pos := p.list.Position
	pos.Length = 0
	if pos != m.at || p.jumping || !p.restored {
		m.at, m.still = pos, 0
	} else {
		m.still++
	}
	if !m.known || p.heights == nil {
		return
	}
	if m.still < measureStill {
		if p.measureLeft() {
			// The list may stand still from now on: the next frame comes to
			// see.
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(100 * time.Millisecond)})
		}
		return
	}
	start := time.Now()
	for first := true; first || time.Since(start) < measureBudget; first = false {
		if !p.measureLoaded(l) && !p.measureGap(l) {
			break
		}
	}
	m.took += time.Since(start)
	p.askTargets()
	p.askGap()
	if p.measureLeft() {
		gtx.Execute(op.InvalidateCmd{})
	}
}

// measureLeft tells whether rows are left for the measurer to lay out now,
// or a run past the reserve's end to read.
func (p *chatPage) measureLeft() bool {
	m := &p.measurer
	if p.loadedToMeasure() >= 0 || m.gap != nil {
		return true
	}
	_, cached := p.source.(model.CachedHistory)
	return cached && !m.reading && !m.growing && p.gapWanted()
}

// loadedToMeasure is the index of the loaded row nearest the view that no
// frame laid out at its revision in the history's environment, and the
// measurer did not lay out; -1 for none.
func (p *chatPage) loadedToMeasure() int {
	m := &p.measurer
	if m.loadedDone {
		return -1
	}
	pos := p.list.Position
	center := max(0, min(pos.First-len(p.above)+pos.Count/2, len(p.messages)-1))
	if !pos.BeforeEnd {
		center = len(p.messages) - 1
	}
	for d := 0; d <= max(center, len(p.messages)-1-center); d++ {
		for k, i := range [2]int{center + d, center - d} {
			if i < 0 || i >= len(p.messages) || k == 1 && d == 0 {
				continue
			}
			msg := p.messages[i]
			if msg.Local() {
				continue
			}
			if l, ok := p.measures[msg.Key.MessageID]; ok && l.ContentRevision == msg.ContentRevision && (!p.unsaved[msg.Key.MessageID] || p.edgeRow(i)) {
				continue
			}
			if rev, ok := m.tried[msg.Key.MessageID]; ok && rev == msg.ContentRevision && !p.unsaved[msg.Key.MessageID] {
				continue
			}
			return i
		}
	}
	m.loadedDone = true
	return -1
}

// measureLoaded lays out off screen the loaded row nearest the view that
// no frame laid out; it tells whether there was one.
func (p *chatPage) measureLoaded(l localization.Catalog) bool {
	i := p.loadedToMeasure()
	if i < 0 {
		return false
	}
	m := &p.measurer
	msg := p.messages[i]
	if m.tried == nil {
		m.tried = map[model.MessageID]uint64{}
	}
	// Marked before it is laid out: a row that panics is not tried again.
	m.tried[msg.Key.MessageID] = msg.ContentRevision
	height, sure := p.measureRow(msg, p.dayStart[i], p.joins[i], l)
	total := height
	row := len(p.above) + i
	if row == len(p.above)+len(p.messages)+len(p.below)-1 {
		total += p.tail
	}
	p.heights.Set(row, total)
	if !sure {
		// Laid out again once the message its reply names is read.
		if m.unsure == nil {
			m.unsure = map[model.MessageID]model.MessageID{}
		}
		m.unsure[msg.Key.MessageID] = m.missing
		p.logNotKept(msg)
		// Not laid out again before it is read, from an end or not.
		delete(p.unsaved, msg.Key.MessageID)
		return true
	}
	p.keepMeasure(msg, height, p.env, !p.edgeRow(i))
	p.noteOffScreen(msg.Key.MessageID, height)
	return true
}

// logNotKept tells the history's journal of msg's row, laid out off screen
// but not kept: the message its reply names was not at hand.
func (p *chatPage) logNotKept(msg model.Message) {
	if historylog.On() {
		historylog.Printf("measure: chat %d message %d at revision %d not kept: it replies to %d, which the cache does not have", p.chat, msg.Key.MessageID, msg.ContentRevision, p.measurer.missing)
	}
}

// noteOffScreen keeps how high a row was laid out off screen, for
// laidOutOnScreen.
func (p *chatPage) noteOffScreen(id model.MessageID, height int) {
	m := &p.measurer
	if !historylog.On() {
		return
	}
	if m.offScreen == nil {
		m.offScreen = map[model.MessageID]int{}
	}
	m.offScreen[id] = height
}

// measureRow lays message msg's row out off screen, in the context of the
// list's rows, its media not loaded and the messages its reply names not
// looked up: how high it is, and whether it had what it names at hand.
func (p *chatPage) measureRow(msg model.Message, date bool, join bubbleJoin, l localization.Catalog) (height int, sure bool) {
	m := &p.measurer
	gtx := m.gtx
	m.ops.Reset()
	gtx.Ops = &m.ops
	gtx.Source = input.Source{}
	p.measuring, m.missing = true, 0
	p.quiet(true)
	// Undone in a defer: a row that panics leaves the frames after it as
	// they were.
	defer func() {
		p.measuring = false
		p.quiet(false)
	}()
	dims := p.row(gtx, msg, date, join, l, false)
	m.laidOut++
	return dims.Size.Y, m.missing == 0
}

// quiet makes the page's media and the avatars' start no load, or lets
// them again.
func (p *chatPage) quiet(quiet bool) {
	if p.media != nil {
		p.media.SetQuiet(quiet)
	}
	if p.quietAvatars != nil {
		p.quietAvatars(quiet)
	}
}

// measuredReference is referenced for a row laid out off screen: the
// message loaded, in the run measured, or read from the cache, or else
// loading, and the row unsure.
func (p *chatPage) measuredReference(id model.MessageID) (model.Message, model.LookupState, bool) {
	if m, ok := p.messageByID(id); ok {
		return m, model.LookupFound, true
	}
	m := &p.measurer
	for _, g := range m.gap {
		if g.Key.MessageID == id {
			return g, model.LookupFound, true
		}
		for _, part := range g.Attachments {
			if part.Key.MessageID == id {
				return part, model.LookupFound, true
			}
		}
	}
	if t, ok := m.targets[id]; ok && t.known {
		return t.msg, t.state, true
	}
	m.missing = id
	if _, ok := m.targets[id]; !ok && id > 0 {
		if m.wanted == nil {
			m.wanted = map[model.MessageID]bool{}
		}
		m.wanted[id] = true
	}
	return model.Message{}, model.LookupLoading, true
}

// askTargets reads from the cache the messages the replies of rows laid
// out off screen name, when it can and none is read.
func (p *chatPage) askTargets() {
	m := &p.measurer
	if len(m.wanted) == 0 || m.reading {
		return
	}
	source, ok := p.source.(model.CachedHistory)
	if !ok {
		m.wanted = nil
		return
	}
	ids := make([]model.MessageID, 0, len(m.wanted))
	for id := range m.wanted {
		ids = append(ids, id)
	}
	m.wanted = nil
	p.startMeasureRead(func(r *measureRead) {
		r.targets = readTargets(source, r.chat, ids)
	}, 0, false)
}

// readTargets reads from source the messages of chat with ids, as the
// cache keeps them.
func readTargets(source model.CachedHistory, chat int64, ids []model.MessageID) map[model.MessageID]measuredTarget {
	out := make(map[model.MessageID]measuredTarget, len(ids))
	for _, id := range ids {
		msg, state, known := source.CachedLookup(chat, id)
		out[id] = measuredTarget{msg: msg, state: state, known: known}
	}
	return out
}

// startMeasureRead reads the cache off the frame, by read, for the
// measurer: the run from from on when run is set.
func (p *chatPage) startMeasureRead(read func(*measureRead), from model.MessageID, run bool) {
	m := &p.measurer
	if m.done == nil {
		m.done = make(chan measureRead, 1)
	}
	m.reading = true
	done, chat, env, invalidate := m.done, p.chat, p.env, p.invalidate
	go func() {
		r := measureRead{chat: chat, env: env, from: from, run: run}
		defer crash.Recover("history measure read", func(*crash.Panic) {
			// Nothing read: the measurer goes on without it.
			done <- measureRead{chat: chat, env: env, from: from, run: run}
			invalidate()
		})
		read(&r)
		done <- r
		invalidate()
	}()
}

// takeMeasureRead takes a read of the cache that came, when it is of the
// chat and environment the history is in.
func (p *chatPage) takeMeasureRead() {
	m := &p.measurer
	var r measureRead
	select {
	case r = <-m.done:
	default:
		return
	}
	m.reading = false
	if r.chat != p.chat || r.env != p.env {
		return
	}
	if m.targets == nil {
		m.targets = map[model.MessageID]measuredTarget{}
	}
	for id, t := range r.targets {
		m.targets[id] = t
	}
	// The loaded rows that lacked them are laid out again, once the cache
	// had them.
	for id, target := range m.unsure {
		if t, ok := r.targets[target]; ok && t.known {
			delete(m.tried, id)
			delete(m.unsure, id)
			m.loadedDone = false
		}
	}
	if !r.run {
		return
	}
	if r.from != p.reserveEnd() || !p.gapWanted() {
		return
	}
	p.takeGap(r)
}

// reserveEnd is the message of the last row of the history with its
// reserve: the reserve's last below what is loaded, or the last loaded.
func (p *chatPage) reserveEnd() model.MessageID {
	if n := len(p.below); n > 0 {
		return p.below[n-1].id
	}
	if n := len(p.messages); n > 0 {
		return p.messages[n-1].Key.MessageID
	}
	return 0
}

// gapWanted tells whether the reserve ended at a row of no height known,
// before the end of what the cache keeps whole, and the measurer is to
// measure on from there.
func (p *chatPage) gapWanted() bool {
	r := &p.reserve
	return r.taken && r.stopped && r.chat == p.chat && r.env == p.env && p.newerBelow && len(p.messages) > 0 && len(r.rows) < reserveLimit
}

// askGap reads from the cache the run past the reserve's end, when it ended
// at a row of no height known and the loaded rows are all laid out.
func (p *chatPage) askGap() {
	m := &p.measurer
	if m.gap != nil || m.reading || m.growing || !p.gapWanted() || p.loadedToMeasure() >= 0 {
		return
	}
	source, ok := p.source.(model.CachedHistory)
	if !ok {
		return
	}
	from := p.reserveEnd()
	p.startMeasureRead(func(r *measureRead) {
		r.msgs = source.CachedFrom(r.chat, from, measureRun)
		have := make(map[model.MessageID]bool, len(r.msgs))
		for _, msg := range r.msgs {
			have[msg.Key.MessageID] = true
		}
		var ids []model.MessageID
		for _, msg := range r.msgs {
			if id := msg.ReplyToMessageID; id > 0 && !have[id] {
				ids = append(ids, id)
			}
		}
		r.targets = readTargets(source, r.chat, ids)
	}, from, true)
}

// takeGap takes the run the cache keeps past the reserve's end, read for
// the measurer: grouped and filtered as the history's messages are.
func (p *chatPage) takeGap(r measureRead) {
	m := &p.measurer
	whole := len(r.msgs) < measureRun
	filtered := p.filtered
	msgs := p.filterMessages(model.GroupAlbums(r.msgs), p.chat)
	p.filtered = filtered
	if len(msgs) == 0 || msgs[0].Key.MessageID != r.from {
		// The cache no longer keeps the reserve's last row: the reserve ends
		// there.
		p.reserve.stopped = false
		logMeasure(p.chat, r.from, 0, "the cache keeps no run from it")
		return
	}
	if len(msgs) < 2 {
		p.reserve.stopped = false
		return
	}
	m.gap, m.whole, m.next, m.laid = msgs, whole, 1, nil
	m.joins = messageJoins(msgs, p.kind == model.KindSaved)
	m.days = make([]bool, len(msgs))
	for i := 1; i < len(msgs); i++ {
		m.days[i] = !sameDay(msgs[i].Date, msgs[i-1].Date)
	}
}

// measureGap lays out off screen the next row of the run past the
// reserve's end; it tells whether there was one. At the end of the run,
// or at a row whose height is known, the reserve grows by the rows laid
// out, and its own read goes on after them.
func (p *chatPage) measureGap(l localization.Catalog) bool {
	m := &p.measurer
	if m.gap == nil {
		return false
	}
	end := len(m.gap) - 1
	if m.whole {
		end = len(m.gap)
	}
	if m.next >= end || m.next > 1 && p.rowKnown(m.gap[m.next]) {
		p.growByGap()
		return true
	}
	i := m.next
	m.next++
	msg := m.gap[i]
	_, loaded := p.rows[msg.Key.MessageID]
	height, sure := p.measureRow(msg, m.days[i], m.joins[i], l)
	if !sure {
		p.logNotKept(msg)
	}
	if sure {
		p.keepMeasure(msg, height, p.env, true)
		p.noteOffScreen(msg.Key.MessageID, height)
	}
	s := skeletonRow{id: msg.Key.MessageID, height: height, outgoing: msg.Outgoing, service: msg.ServicePill()}
	if r := p.rows[msg.Key.MessageID]; r != nil {
		if p.senderAvatar(msg) != 0 && p.avatar != nil {
			s.slot = m.gtx.Dp(34) + m.gtx.Dp(8)
		}
		s.bubble = image.Pt(max(0, r.bodySize.X-s.slot), r.bodySize.Y)
		s.shape = shapeMeasured
		if !loaded {
			// The row is not the history's: it is laid out once.
			delete(p.rows, msg.Key.MessageID)
			delete(p.polls, msg.Key.MessageID)
		}
	}
	m.laid = append(m.laid, s)
	return true
}

// rowKnown tells whether the height of msg's row in the history's
// environment is known: kept at its width, or told by its profile.
func (p *chatPage) rowKnown(msg model.Message) bool {
	if l, ok := p.measures[msg.Key.MessageID]; ok && l.ContentRevision == msg.ContentRevision {
		return true
	}
	return p.profiled[msg.Key.MessageID] == msg.ContentRevision
}

// growByGap adds the rows of the run laid out to the reserve, and has the
// reserve read on after them where the run did not end the cache's.
func (p *chatPage) growByGap() {
	m := &p.measurer
	rows, whole := m.laid, m.whole && m.next >= len(m.gap)
	m.dropGap()
	p.keepShapes(rows)
	last := p.reserveEnd()
	for _, s := range rows {
		if s.id > last {
			p.reserve.rows = append(p.reserve.rows, s)
			if p.reserve.heights == nil {
				p.reserve.heights = map[model.MessageID]int{}
			}
			p.reserve.heights[s.id] = s.height
		}
	}
	p.reserveGrown = len(rows) > 0
	end := "the end of the run the cache keeps whole"
	if !whole {
		end = "a row of a height known"
	}
	if n := len(rows); n > 0 {
		logMeasure(p.chat, rows[0].id, n, end)
	}
	if whole || len(rows) == 0 {
		p.reserve.stopped = false
		return
	}
	p.growReserve(rows[len(rows)-1].id)
}

// logMeasure tells the history's journal how many rows past the reserve's
// end, from message from on, the measurer laid out, and what it ended at.
func logMeasure(chat int64, from model.MessageID, n int, end string) {
	if historylog.On() {
		historylog.Printf("measure: chat %d from %d: %d rows past the reserve laid out off screen, up to %s", chat, from, n, end)
	}
}
