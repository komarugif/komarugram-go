// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"math/rand/v2"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"gio-mw/token"
	"gio-mw/wdk"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// A message erased while it is in view leaves it as in Telegram for
// Android (docs/PORT_ANDROID_EFFECTS.md): it crumbles into dust that drifts
// away, the "Thanos" effect, or fades out, as the settings choose, while
// the messages around it move into its place. The history is a snapshot,
// which a message also leaves when what is loaded moves: the store tells
// which of those gone were erased (model.ErasedMessages). With animations
// off a message goes at once, as it does from a history out of view.

const (
	// leaveMove is how long the messages around one that left take to
	// move into its place, and how long one that fades takes.
	leaveMove = 250 * time.Millisecond
	// leaveHold is how long the rows around messages that dissolve stay
	// where they were before they move: the dust is seen over the place
	// the messages left, not over the rows that take it. Rows around a
	// message that fades wait for it to fade, leaveMove.
	leaveHold = time.Second
	// leaveMinAge is how old a message of a group or of a bot's chat is to
	// leave with an effect: what a bot deletes as it comes just goes.
	leaveMinAge = 2 * time.Second
	// leavePictureWait is how long the picture of the messages to
	// dissolve is waited for; they fade when it does not come.
	leavePictureWait = 500 * time.Millisecond
)

// leaveEasing is the curve Android moves and fades a history's rows by.
var leaveEasing = token.Easing{X1: 0.199, Y1: 0.011, X2: 0.279, Y2: 0.910}

// goneRow is a message erased while it was in view.
type goneRow struct {
	msg  model.Message
	join bubbleJoin
	// row is what the history kept of its row, for it to be drawn as it
	// was.
	row *messageRow
	// body is where its bubble was in the history's view when it went,
	// and sprite where it is in the picture of the messages that went
	// with it.
	body, sprite image.Rectangle
}

// leaving are the messages erased together, on their way out of view.
type leaving struct {
	rows    []goneRow
	started time.Time
	// dissolves is set while they are to crumble into dust: until the
	// picture of them comes they stand as they were.
	dissolves bool
	picture   chan dissolvePicture
	dust      *dissolve
	// anchor is a message that stays, which they follow as the history
	// scrolls: at is its index when looked for last, base where its row's
	// place was when they went, or when rows began to move to other
	// places since, kept how far they had scrolled by then, and scrolled
	// how far they have by now.
	anchor         model.MessageID
	at             int
	base           int
	based          bool
	kept, scrolled int
}

// dissolvePicture is the picture of the messages to dissolve, as the GPU
// drew them off the frame.
type dissolvePicture struct {
	img *image.RGBA
	err error
}

// rowMoves are the rows on their way to where the history has them now,
// from where they were before a message among them left.
type rowMoves struct {
	started time.Time
	// olds are where the rows' tops were in the view; shifts, how far
	// each was from where it is now. The shifts are guessed before the
	// frame that lays the history out anew, and taken from it after: the
	// olds are let go of then.
	olds   map[model.MessageID]int
	shifts map[model.MessageID]int
}

// leavers are the rows in view that next, the messages the history is to
// show, lacks because their messages were erased; and tops, where the top
// of every row in view is, for those that stay to move from.
func (p *chatPage) leavers(gtx layout.Context, next []model.Message, env model.RenderEnvironment) (rows []goneRow, tops map[model.MessageID]int, anchor model.MessageID) {
	source, ok := p.source.(model.ErasedMessages)
	if !ok || !p.animate || !wdk.AnimationsEnabled(gtx) || !p.restored || p.heights == nil || env != p.env || len(next) >= len(p.messages) {
		return nil, nil, 0
	}
	above := len(p.above)
	pos := p.list.Position
	lo, hi := max(0, pos.First-above), min(len(p.messages), pos.First-above+pos.Count)
	if lo >= hi {
		return nil, nil, 0
	}
	// An album that lost a part is another row of the same messages.
	stays := make(map[model.MessageID]bool, len(next))
	for _, m := range next {
		stays[m.Key.MessageID] = true
		for _, part := range m.Attachments {
			stays[part.Key.MessageID] = true
		}
	}
	tops = map[model.MessageID]int{}
	last := -1
	for i := lo; i < hi; i++ {
		m := p.messages[i]
		id := m.Key.MessageID
		top := int(p.heights.Prefix(above+i)-p.heights.Prefix(pos.First)) - pos.Offset + p.moveShift(gtx, id)
		if stays[id] {
			tops[id] = top
			if anchor == 0 || last == i-1 {
				// The one after those that leave, or the first in view.
				anchor = id
			}
			continue
		}
		r := p.rows[id]
		if r == nil || r.bodySize.X <= 0 || r.bodySize.Y <= 0 || m.Local() || m.Deleted {
			continue
		}
		if (p.kind == model.KindGroup || p.kind == model.KindBot) && gtx.Now.Sub(m.Date) < leaveMinAge {
			continue
		}
		erased := true
		for _, part := range messageParts(m) {
			if stays[part.Key.MessageID] || !source.MessageErased(part.Key) {
				erased = false
				break
			}
		}
		if !erased {
			continue
		}
		last = i
		rows = append(rows, goneRow{msg: m, join: p.joins[i], row: r, body: image.Rectangle{Max: r.bodySize}.Add(image.Pt(r.bodyLeft, top+r.bodyTop))})
	}
	if len(rows) == 0 {
		return nil, nil, 0
	}
	return rows, tops, anchor
}

// startLeaving sets rows leaving the view, and the rows that stay moving
// from tops into their places in the history as it is now.
func (p *chatPage) startLeaving(gtx layout.Context, rows []goneRow, tops map[model.MessageID]int, anchor model.MessageID) {
	if len(rows) == 0 {
		return
	}
	dissolves := p.deleteEffect == nil || p.deleteEffect() == preferences.DeleteDissolve
	// The rows stay until what left is gone, or nearly.
	hold := leaveMove
	if dissolves {
		hold = leaveHold
	}
	p.moves = &rowMoves{started: gtx.Now.Add(hold), olds: tops, shifts: map[model.MessageID]int{}}
	p.placeMoves(false)
	for _, lv := range p.leavings {
		// The rows move to other places, not the history under those
		// that leave: they stay where they are.
		lv.kept, lv.based = lv.scrolled, false
	}
	p.leavings = append(p.leavings, &leaving{rows: rows, started: gtx.Now, dissolves: dissolves, anchor: anchor})
}

// placeMoves works the shifts of the rows on their way out from where
// they were: against where the list will put them, as it is set to before
// its frame, or where it did put them, exact, after it.
func (p *chatPage) placeMoves(exact bool) {
	m := p.moves
	if m == nil || m.olds == nil || p.heights == nil {
		return
	}
	above := len(p.above)
	pos := p.list.Position
	base := p.heights.Prefix(pos.First) + int64(pos.Offset)
	if end := max(0, p.heights.Total()-int64(p.viewHeight)); !exact && (!pos.BeforeEnd || base > end) {
		// At its end, the history ends at the bottom of the view; and
		// shorter by a row, it does not scroll past that.
		base = end
	}
	clear(m.shifts)
	for i, msg := range p.messages {
		if old, ok := m.olds[msg.Key.MessageID]; ok {
			m.shifts[msg.Key.MessageID] = old - int(p.heights.Prefix(above+i)-base)
		}
	}
	if exact {
		m.olds = nil
	}
}

// moveShift is how far below its place the row of message id is drawn in
// this frame, on its way there.
func (p *chatPage) moveShift(gtx layout.Context, id model.MessageID) int {
	m := p.moves
	if m == nil {
		return 0
	}
	// Before they start, the rows are where they were.
	t := max(0, float64(gtx.Now.Sub(m.started))/float64(leaveMove))
	if t >= 1 || !wdk.AnimationsEnabled(gtx) {
		p.moves = nil
		return 0
	}
	shift, ok := m.shifts[id]
	if !ok {
		shift = m.near(id)
	}
	if shift == 0 {
		return 0
	}
	gtx.Execute(op.InvalidateCmd{})
	return int(math.Round(float64(shift) * (1 - leaveEasing.Ease(t))))
}

// near is the shift of a row that was out of view: that of the nearest
// row before it that was in view, or of the first of those when it is
// before them all.
func (m *rowMoves) near(id model.MessageID) int {
	// Messages on their way, of negative IDs, are after the others.
	order := func(id model.MessageID) int64 {
		if id < 0 {
			return math.MaxInt64/2 - int64(id)
		}
		return int64(id)
	}
	at := order(id)
	var before, first int64 = -1, math.MaxInt64
	var shift, firstShift int
	for known, s := range m.shifts {
		k := order(known)
		if k < at && k > before {
			before, shift = k, s
		}
		if k < first {
			first, firstShift = k, s
		}
	}
	if before < 0 {
		return firstShift
	}
	return shift
}

// dropLeaving forgets the messages leaving and the rows moving: they are
// where the history has them.
func (p *chatPage) dropLeaving() {
	for _, lv := range p.leavings {
		p.forgetGone(lv)
	}
	p.leavings, p.moves, p.leavingOut = nil, nil, false
}

// forgetGone lets go of what the history kept of the rows of lv.
func (p *chatPage) forgetGone(lv *leaving) {
	for _, g := range lv.rows {
		if id := g.msg.Key.MessageID; !p.hasMessage(id) {
			delete(p.rows, id)
		}
	}
}

// layoutLeaving draws the messages leaving the view over the history, in
// the context the list lays its rows out in.
func (p *chatPage) layoutLeaving(gtx layout.Context, l localization.Catalog) {
	p.placeMoves(true)
	if len(p.leavings) == 0 {
		return
	}
	if !wdk.AnimationsEnabled(gtx) {
		p.dropLeaving()
		return
	}
	// Cleared after the layout, not in a defer: a panic in it leaves the
	// flag for recoverLeaving.
	p.leavingOut = true
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	rows := gtx
	if p.measurer.known {
		rows = p.measurer.gtx
		rows.Ops, rows.Now = gtx.Ops, gtx.Now
	} else {
		rows.Constraints = layout.Constraints{Min: image.Pt(gtx.Constraints.Max.X, 0), Max: image.Pt(gtx.Constraints.Max.X, snapshotMaxHeight)}
	}
	rows = rows.Disabled()
	p.rowTopKnown = false
	kept := p.leavings[:0]
	for _, lv := range p.leavings {
		if p.layoutGone(gtx, rows, lv, l) {
			kept = append(kept, lv)
		} else {
			p.forgetGone(lv)
		}
	}
	clear(p.leavings[len(kept):])
	p.leavings = kept
	if len(kept) > 0 {
		gtx.Execute(op.InvalidateCmd{})
	}
	p.leavingOut = false
}

// recoverLeaving drops the messages leaving when the layout of them
// panicked, and reports whether it did.
func (p *chatPage) recoverLeaving() bool {
	if !p.leavingOut {
		return false
	}
	p.dropLeaving()
	return true
}

// layoutGone draws the messages of lv on their way out, and reports
// whether they are still on it. rows is the context of a row.
func (p *chatPage) layoutGone(gtx, rows layout.Context, lv *leaving, l localization.Catalog) bool {
	p.follow(lv)
	defer op.Offset(image.Pt(0, lv.scrolled)).Push(gtx.Ops).Pop()
	if lv.dissolves && lv.dust == nil {
		switch {
		case lv.picture == nil:
			p.askPicture(rows, lv, l)
		case gtx.Now.Sub(lv.started) > leavePictureWait:
			lv.dissolves = false
		default:
			select {
			case pic := <-lv.picture:
				if pic.err == nil {
					lv.dust = newDissolve(gtx, lv, pic.img)
				}
				lv.dissolves = lv.dust != nil
			default:
			}
		}
		if !lv.dissolves {
			// No picture of them: they fade from now.
			lv.started, lv.picture = gtx.Now, nil
		}
	}
	if lv.dust != nil {
		return lv.dust.layout(gtx)
	}
	opacity := float32(1)
	if !lv.dissolves {
		t := float64(gtx.Now.Sub(lv.started)) / float64(leaveMove)
		if t >= 1 {
			return false
		}
		opacity = float32(1 - leaveEasing.Ease(t))
	}
	for i := range lv.rows {
		g := &lv.rows[i]
		record := op.Record(gtx.Ops)
		r := p.goneRow(rows, g, l)
		call := record.Stop()
		// Its bubble where it was, whatever the row has over it now.
		stack := op.Offset(g.body.Min.Sub(image.Pt(r.bodyLeft, r.bodyTop))).Push(gtx.Ops)
		fade := paint.PushOpacity(gtx.Ops, opacity)
		call.Add(gtx.Ops)
		fade.Pop()
		stack.Pop()
	}
	return true
}

// goneRow lays out the row of a message that left as it was, without the
// date over it, and returns what the history keeps of it.
func (p *chatPage) goneRow(gtx layout.Context, g *goneRow, l localization.Catalog) *messageRow {
	id := g.msg.Key.MessageID
	// The history forgets the rows of messages it has not.
	p.rows[id] = g.row
	p.row(gtx, g.msg, false, g.join, l, false)
	g.row = p.rows[id]
	return g.row
}

// follow moves lv with the history as it scrolls.
func (p *chatPage) follow(lv *leaving) {
	if lv.anchor == 0 || p.heights == nil {
		return
	}
	if lv.at >= len(p.messages) || p.messages[lv.at].Key.MessageID != lv.anchor {
		lv.at = -1
		for i, m := range p.messages {
			if m.Key.MessageID == lv.anchor {
				lv.at = i
				break
			}
		}
		if lv.at < 0 {
			// It left too: they stay where they are.
			lv.anchor, lv.at = 0, 0
			return
		}
	}
	pos := p.list.Position
	// Its row's place, not where the row is on its way there.
	top := int(p.heights.Prefix(len(p.above)+lv.at)-p.heights.Prefix(pos.First)) - pos.Offset
	if !lv.based {
		lv.base, lv.based = top, true
	}
	lv.scrolled = lv.kept + top - lv.base
}

// askPicture has the GPU draw the messages of lv off the frame, one under
// the other, for them to dissolve.
func (p *chatPage) askPicture(rows layout.Context, lv *leaving, l localization.Catalog) {
	ops := new(op.Ops)
	// Without the spoilers' dust: its picture changes while this one is
	// drawn.
	sgtx := withoutEffects(rows)
	sgtx.Ops = ops
	y := 0
	for i := range lv.rows {
		g := &lv.rows[i]
		stack := op.Offset(image.Pt(0, y)).Push(ops)
		record := op.Record(ops)
		r := p.goneRow(sgtx, g, l)
		call := record.Stop()
		// Its bubble alone: what the row has beside it stays.
		g.sprite = image.Rectangle{Max: r.bodySize}.Add(image.Pt(r.bodyLeft, y))
		area := clip.Rect{Max: image.Pt(sgtx.Constraints.Max.X, r.bodySize.Y)}.Push(ops)
		shift := op.Offset(image.Pt(0, -r.bodyTop)).Push(ops)
		call.Add(ops)
		shift.Pop()
		area.Pop()
		stack.Pop()
		y += r.bodySize.Y
	}
	size := image.Pt(sgtx.Constraints.Max.X, y)
	if size.X <= 0 || y <= 0 || y > snapshotMaxHeight {
		lv.dissolves = false
		return
	}
	results := make(chan dissolvePicture, 1)
	lv.picture = results
	invalidate := p.invalidate
	go func() {
		defer crash.Recover("dissolve picture", func(e *crash.Panic) { results <- dissolvePicture{err: e}; invalidate() })
		img, err := renderSnapshot(ops, size)
		results <- dissolvePicture{img, err}
		invalidate()
	}()
}

const (
	// dissolveParticles is how many pieces the messages that dissolve
	// together are cut into at most: larger ones are cut coarser.
	dissolveParticles = 100000
	// dissolvePace is how fast the effect's time runs.
	dissolvePace = 1.15
	// dissolveSweep is how long the crumbling takes to cross a bubble
	// from its left edge to its right, in the effect's time; a piece
	// starts up to dissolveJitter later, for the edge not to be a line.
	dissolveSweep  = 0.6
	dissolveJitter = 0.08
	// A piece starts at dissolveSlow…dissolveFast dp a unit of time, its
	// own way, rises faster and faster by dissolveRise, and is pushed on
	// sideways by dissolvePush while the effect is younger than
	// dissolvePushed.
	dissolveSlow   = 26
	dissolveFast   = 52
	dissolveRise   = 65
	dissolvePush   = 19
	dissolvePushed = 0.35
	// It lives dissolveLifeMin…dissolveLifeMax units once it moves, and
	// fades out over the last dissolveFade of them.
	dissolveLifeMin = 0.5
	dissolveLifeMax = 1.1
	dissolveFade    = 0.45
)

// The room the pieces fly into around the bubbles, as far as they get.
const (
	dissolveRoomSide = unit.Dp(64)
	dissolveRoomUp   = unit.Dp(104)
	dissolveRoomDown = unit.Dp(24)
)

// dissolvePart is a piece of a bubble: a square of its picture that flies
// off.
type dissolvePart struct {
	// x and y are where it is in the canvas, vx and vy how fast it
	// moves, in px to a unit of the effect's time.
	x, y, vx, vy float32
	// delay is when it starts to move, and life how long it does.
	delay, life float32
	// src is where its square begins in the picture's pixels.
	src int32
}

// dissolve is the dust the messages that left together crumble into: the
// picture of their bubbles cut into small squares that fly off one after
// another, from the left edge of each bubble to its right, drawn by the
// CPU into one picture over the history.
type dissolve struct {
	sprite *image.RGBA
	// cell is the side of a piece, in px.
	cell  int
	parts []dissolvePart
	// rect is the canvas's place in the history's view, as it was when
	// the messages left.
	rect   image.Rectangle
	canvas *image.RGBA
	// time is the effect's, last when it was moved on, and end when its
	// last piece is gone.
	time  float32
	last  time.Time
	end   float32
	scale float32
}

// newDissolve cuts the picture img of the messages of lv into pieces; nil
// when none of them is in view.
func newDissolve(gtx layout.Context, lv *leaving, img *image.RGBA) *dissolve {
	var bounds image.Rectangle
	area := 0
	for _, g := range lv.rows {
		bounds = bounds.Union(g.body)
		area += g.sprite.Dx() * g.sprite.Dy()
	}
	side, up, down := gtx.Dp(dissolveRoomSide), gtx.Dp(dissolveRoomUp), gtx.Dp(dissolveRoomDown)
	view := image.Rectangle{Max: gtx.Constraints.Max}.Sub(image.Pt(0, lv.scrolled))
	rect := image.Rect(bounds.Min.X-side, bounds.Min.Y-up, bounds.Max.X+side, bounds.Max.Y+down).Intersect(view)
	if rect.Empty() || area == 0 {
		return nil
	}
	scale := gtx.Metric.PxPerDp
	// About a piece to 0.4 dp squared, or a pixel, as Android cuts them.
	cell := max(1, int(0.4*scale+.5))
	for area/(cell*cell) > dissolveParticles {
		cell++
	}
	d := &dissolve{sprite: img, cell: cell, rect: rect, canvas: image.NewRGBA(image.Rectangle{Max: rect.Size()}), scale: scale, last: gtx.Now}
	d.parts = make([]dissolvePart, 0, area/(cell*cell))
	rng := rand.New(rand.NewPCG(0x5eed, uint64(len(lv.rows))<<32|uint64(uint32(lv.rows[0].msg.Key.MessageID))))
	for _, g := range lv.rows {
		sprite := g.sprite.Intersect(img.Bounds())
		at := g.body.Min.Sub(rect.Min)
		width := float32(max(1, sprite.Dx()))
		for y := 0; y+cell <= sprite.Dy(); y += cell {
			for x := 0; x+cell <= sprite.Dx(); x += cell {
				src := img.PixOffset(sprite.Min.X+x, sprite.Min.Y+y)
				// Nothing of the bubble in the corner of its box.
				if img.Pix[src+3] == 0 && img.Pix[src+(cell-1)*(img.Stride+4)+3] == 0 {
					continue
				}
				angle := rng.Float64() * 2 * math.Pi
				speed := (dissolveSlow + rng.Float32()*(dissolveFast-dissolveSlow)) * scale
				part := dissolvePart{
					x: float32(at.X + x), y: float32(at.Y + y),
					vx: float32(math.Cos(angle)) * speed, vy: float32(math.Sin(angle)) * speed,
					delay: float32(x)/width*dissolveSweep + rng.Float32()*dissolveJitter,
					life:  dissolveLifeMin + rng.Float32()*(dissolveLifeMax-dissolveLifeMin),
					src:   int32(src),
				}
				d.end = max(d.end, part.delay+part.life)
				d.parts = append(d.parts, part)
			}
		}
	}
	if len(d.parts) == 0 {
		return nil
	}
	return d
}

// layout moves the pieces on to the frame's time and draws them; false
// once they are all gone.
func (d *dissolve) layout(gtx layout.Context) bool {
	// A frame that came late moves them as one a tenth of a second
	// after the last.
	dt := float32(min(gtx.Now.Sub(d.last).Seconds(), 0.1)) * dissolvePace
	d.last = gtx.Now
	if dt > 0 {
		d.step(dt)
	}
	if d.time >= d.end {
		return false
	}
	d.draw()
	defer op.Offset(d.rect.Min).Push(gtx.Ops).Pop()
	defer clip.Rect{Max: d.rect.Size()}.Push(gtx.Ops).Pop()
	// The canvas drawn anew is another picture to the GPU.
	paint.NewImageOp(d.canvas).Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	return true
}

// step moves the pieces by dt of the effect's time.
func (d *dissolve) step(dt float32) {
	from := d.time
	d.time += dt
	rise := dissolveRise * d.scale
	// The push sideways is at its strongest as the effect begins.
	push := dissolvePush * d.scale * max(0, 1-(from+dt/2)/dissolvePushed)
	for i := range d.parts {
		p := &d.parts[i]
		age := d.time - p.delay
		if age <= 0 || age-dt >= p.life {
			continue
		}
		// It moved for the part of the step after its delay.
		h := min(dt, age)
		p.vy -= rise * h
		if p.vx < 0 {
			p.vx -= push * h
		} else {
			p.vx += push * h
		}
		p.x += p.vx * h
		p.y += p.vy * h
	}
}

// draw draws the pieces into the canvas: those at rest as the bubble
// they are of, and over them those that fly.
func (d *dissolve) draw() {
	clear(d.canvas.Pix)
	size := d.canvas.Bounds().Size()
	dst, src := d.canvas.Pix, d.sprite.Pix
	stride, from := d.canvas.Stride, d.sprite.Stride
	cell := d.cell
	for _, flying := range []bool{false, true} {
		for i := range d.parts {
			p := &d.parts[i]
			age := d.time - p.delay
			if (age > 0) != flying || age >= p.life {
				continue
			}
			x, y := int(math.Floor(float64(p.x+.5))), int(math.Floor(float64(p.y+.5)))
			if x < 0 || y < 0 || x+cell > size.X || y+cell > size.Y {
				continue
			}
			alpha := uint32(255)
			if flying {
				alpha = uint32(min(1, (p.life-age)/dissolveFade) * 255)
			}
			for cy := range cell {
				s := src[int(p.src)+cy*from:]
				t := dst[(y+cy)*stride+x*4:]
				for cx := 0; cx < cell*4; cx += 4 {
					// Premultiplied, one over the other.
					a := uint32(s[cx+3]) * alpha / 255
					if a == 0 {
						continue
					}
					rest := 255 - a
					t[cx] = uint8(uint32(s[cx])*alpha/255 + uint32(t[cx])*rest/255)
					t[cx+1] = uint8(uint32(s[cx+1])*alpha/255 + uint32(t[cx+1])*rest/255)
					t[cx+2] = uint8(uint32(s[cx+2])*alpha/255 + uint32(t[cx+2])*rest/255)
					t[cx+3] = uint8(a + uint32(t[cx+3])*rest/255)
				}
			}
		}
	}
}
