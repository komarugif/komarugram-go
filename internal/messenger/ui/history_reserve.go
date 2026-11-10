// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// The history reserves the room of the rows below what it has loaded, as
// far as the cache keeps the chat's history whole and knows those rows'
// heights at the width it is: laid out at it before, or told by their
// profiles (row_profile.go). The list goes on past its messages with
// them, drawn as skeletons until their pages come, and its scrollbar is
// as long as they reach. Above where the history opened, pages come as
// they did: the scrollbar does not grow under the hand that drags it up.

// reserveLimit is the most rows a history reserves below it.
const reserveLimit = 20000

// skeletonRow is a row of the reserve: its message, how high the row is,
// how large its bubble, whose side it is on and the room an avatar takes
// beside it; a service message's is a plate across the middle.
type skeletonRow struct {
	id       model.MessageID
	height   int
	bubble   image.Point
	outgoing bool
	service  bool
	slot     int
	// shape tells where its bubble came from, for the journal; profile is
	// the row's profile, which tells what the bubble shows
	// (skeleton_shape.go), nil when it is not known.
	shape   shapeFrom
	profile []byte
}

// reserve is what a history reserves below what it has loaded: of a chat
// in an environment, its rows, and the heights they told for the rows
// loaded since, which come in as high. taken is set once the rows came
// for the environment, and stopped while they end at a row of no height
// known, before the end of what the cache keeps whole: the measurer goes
// on from there (history_measure.go).
type reserve struct {
	chat    int64
	env     model.RenderEnvironment
	asked   bool
	taken   bool
	stopped bool
	// room is set while rows have the room of messages let go of
	// (history_window.go).
	room    bool
	rows    []skeletonRow
	heights map[model.MessageID]int
}

// reserveResult is a reserve worked out: for a chat, in an environment;
// whether it stopped at a row of no height known; and, for one that grows
// the reserve, the message it was worked out after.
type reserveResult struct {
	chat    int64
	env     model.RenderEnvironment
	rows    []skeletonRow
	stopped bool
	grow    bool
	after   model.MessageID
}

// after is the rows of the reserve after the last of messages.
func (r *reserve) after(messages []model.Message) []skeletonRow {
	if len(messages) == 0 {
		return nil
	}
	last := messages[len(messages)-1]
	end := last.Key.MessageID
	for _, a := range last.Attachments {
		end = max(end, a.Key.MessageID)
	}
	for i, s := range r.rows {
		if s.id > end {
			return r.rows[i:]
		}
	}
	return nil
}

// askReserve has the rows below the history worked out, once for its chat
// in its environment, where the history goes on below what it has loaded:
// opened above the chat's end, as at a message.
func (p *chatPage) askReserve(history model.History) {
	if !p.restored || !history.HasNewer || len(p.messages) == 0 {
		return
	}
	if p.reserve.asked && p.reserve.chat == p.chat && p.reserve.env == p.env {
		return
	}
	source, ok := p.source.(model.HistoryReserve)
	if !ok {
		return
	}
	if p.reserve.chat != p.chat {
		p.reserve = reserve{}
	}
	// The rows of the reserve worked out for another width stay until the
	// new ones come: the scrollbar keeps its length meanwhile.
	p.reserve.chat, p.reserve.env, p.reserve.asked, p.reserve.taken = p.chat, p.env, true, false
	p.measurer.dropGap()
	last := p.messages[len(p.messages)-1]
	after := last.Key.MessageID
	for _, a := range last.Attachments {
		after = max(after, a.Key.MessageID)
	}
	p.readReserve(source, after, false, reserveLimit)
}

// growReserve has the reserve read on after message after, its last row,
// which the measurer laid out: as far as the cache knows the heights of
// the rows after it.
func (p *chatPage) growReserve(after model.MessageID) {
	source, ok := p.source.(model.HistoryReserve)
	if !ok || len(p.reserve.rows) >= reserveLimit {
		p.reserve.stopped = false
		return
	}
	p.measurer.growing = true
	p.readReserve(source, after, true, reserveLimit-len(p.reserve.rows))
}

// readReserve works the reserve's rows after message after out off the
// frame, at most limit of them: the whole reserve, or, with grow, those
// it grows by.
func (p *chatPage) readReserve(source model.HistoryReserve, after model.MessageID, grow bool, limit int) {
	chat, env := p.chat, p.env
	if p.reserveDone == nil {
		p.reserveDone = make(chan reserveResult, 1)
	}
	done := p.reserveDone
	go func() {
		defer crash.Recover("history reserve", func(e *crash.Panic) {
			// None: the history pages on as it did.
			tellReserve(done, reserveResult{chat: chat, env: env, grow: grow, after: after})
			p.invalidate()
		})
		kept := source.Reserve(chat, after, env, rowProfileVersion, limit)
		rows := reserveRows(kept, env)
		logReserve(chat, after, kept, rows, limit)
		stopped := len(rows)+albumParts(kept, len(rows)) < len(kept)
		tellReserve(done, reserveResult{chat: chat, env: env, rows: rows, stopped: stopped, grow: grow, after: after})
		p.invalidate()
	}()
}

// logReserve tells the history's journal how far the reserve after message
// after reached of the rows the cache kept, and what it ended at.
func logReserve(chat int64, after model.MessageID, kept []model.ReserveRow, rows []skeletonRow, limit int) {
	if !historylog.On() {
		return
	}
	heights, profiles := 0, 0
	for _, r := range kept[:min(len(kept), len(rows)+albumParts(kept, len(rows)))] {
		if r.Height >= 0 {
			heights++
		}
		if r.Profile != nil {
			profiles++
		}
	}
	end := "the end of the run the cache keeps whole"
	if n := len(rows) + albumParts(kept, len(rows)); n < len(kept) {
		r := kept[n]
		end = fmt.Sprintf("message %d at revision %d, of no height at this width nor profile (%s, %s)", r.ID, r.Revision, keptAt("height", r.Height >= 0, r.HeightRevision), keptAt("profile", r.Profile != nil, r.ProfileRevision))
	} else if len(kept) == limit {
		end = fmt.Sprintf("the limit of %d rows", limit)
	}
	historylog.Printf("reserve: chat %d after %d: %d rows of %d kept (%d heights, %d profiles), up to %s", chat, after, len(rows), len(kept), heights, profiles, end)
}

// keptAt tells of what the cache keeps of a row: none, or at which
// revision.
func keptAt(what string, kept bool, revision uint64) string {
	if !kept {
		return "no " + what
	}
	return fmt.Sprintf("a %s at revision %d", what, revision)
}

// albumParts counts the parts of albums past their first among the first
// rows of kept that made n rows of the reserve.
func albumParts(kept []model.ReserveRow, n int) int {
	parts := 0
	var album int64
	for _, r := range kept {
		if r.GroupedID != 0 && r.Media && r.GroupedID == album {
			parts++
			continue
		}
		if n == 0 {
			break
		}
		n--
		album = 0
		if r.GroupedID != 0 && r.Media {
			album = r.GroupedID
		}
	}
	return parts
}

// tellReserve hands r over in done, in place of one there not taken yet.
func tellReserve(done chan reserveResult, r reserveResult) {
	for {
		select {
		case done <- r:
			return
		default:
		}
		select {
		case <-done:
		default:
		}
	}
}

// takeReserve takes a reserve worked out, for the chat and environment the
// history is in; it tells whether the history is to be built again.
func (p *chatPage) takeReserve() bool {
	select {
	case r := <-p.reserveDone:
		if r.grow {
			p.measurer.growing = false
		}
		if r.chat != p.chat || r.env != p.env || p.reserve.chat != r.chat || p.reserve.env != r.env {
			return false
		}
		if r.grow {
			// After the rows the measurer laid out: those of the reserve
			// past them, unless the reserve went another way since.
			if !p.reserve.taken || r.after != p.reserveEnd() && (len(p.reserve.rows) == 0 || r.after != p.reserve.rows[len(p.reserve.rows)-1].id) {
				return false
			}
			last := r.after
			if n := len(p.reserve.rows); n > 0 {
				last = max(last, p.reserve.rows[n-1].id)
			}
			for _, s := range r.rows {
				if s.id > last {
					p.reserve.rows = append(p.reserve.rows, s)
					p.reserve.heights[s.id] = s.height
				}
			}
			p.keepShapes(r.rows)
			p.reserve.stopped = r.stopped
			return len(r.rows) > 0
		}
		// The rows kept of messages let go of stay, with the cache's.
		kept := p.reserve.rows
		p.reserve.rows = r.rows
		p.keepShapes(r.rows)
		p.reserve.taken, p.reserve.stopped = true, r.stopped
		p.reserve.heights = make(map[model.MessageID]int, len(r.rows))
		for _, s := range r.rows {
			p.reserve.heights[s.id] = s.height
		}
		if p.reserve.room {
			var room []skeletonRow
			for _, s := range kept {
				if _, ok := p.reserve.heights[s.id]; !ok {
					room = append(room, s)
				}
			}
			p.reserve.merge(room)
		}
		p.measurer.dropGap()
		return true
	default:
		return false
	}
}

// reserveRows makes skeletons of the rows the cache keeps, laid out in
// env, as far as their heights are known: kept for env's width, or told
// by their profiles. The parts of an album are its first's row.
func reserveRows(rows []model.ReserveRow, env model.RenderEnvironment) []skeletonRow {
	metric := unit.Metric{PxPerDp: float32(env.ScaleMilli) / 1000, PxPerSp: float32(env.TextScaleMilli) / 1000}
	var out []skeletonRow
	var album int64
	for i, r := range rows {
		if r.GroupedID != 0 && r.Media && r.GroupedID == album {
			continue
		}
		album = 0
		// An album's row is of a revision of its parts', not of its first.
		inAlbum := r.GroupedID != 0 && r.Media && i+1 < len(rows) && rows[i+1].GroupedID == r.GroupedID
		if inAlbum {
			album = r.GroupedID
		}
		s := skeletonRow{id: r.ID, outgoing: r.Outgoing, service: r.Service, height: -1}
		if r.Height >= 0 && (r.HeightRevision == r.Revision || inAlbum) {
			s.height = r.Height
		}
		if r.Profile != nil && (r.ProfileRevision == r.Revision || inAlbum) && s.height >= 0 {
			// Its height is kept: the profile tells its shape once it is
			// drawn (skeleton_shape.go).
			s.profile, s.shape = r.Profile, shapeProfile
		} else if r.Profile != nil && (r.ProfileRevision == r.Revision || inAlbum) {
			if prof, ok, err := decodeRowProfile(r.Profile); ok && err == nil {
				h, w, _ := prof.predict(metric, env.WidthPx)
				if s.height < 0 {
					s.height = h
				}
				s.bubble = image.Pt(w, max(0, h-prof.bubble.rest))
				s.slot = prof.bubble.slot
				s.shape = shapeProfile
				s.profile = r.Profile
			}
		}
		if s.height < 0 {
			// Its height is not known: the reserve ends before it, and the
			// history pages on from there.
			break
		}
		out = append(out, s)
	}
	return out
}

// skeleton draws the room of a row of the reserve, and in it the shape of
// its bubble, or of a service message's plate.
func (p *chatPage) skeleton(gtx layout.Context, s skeletonRow) layout.Dimensions {
	size := image.Pt(gtx.Constraints.Max.X, s.height)
	room := min(size.X-gtx.Dp(rowInsetLeft)-gtx.Dp(rowInsetRight), gtx.Dp(bubbleMaxWidth))
	bubble := s.bubble
	if bubble.X <= 0 {
		bubble.X = room * 2 / 3
	}
	if bubble.Y <= 0 {
		bubble.Y = max(0, s.height-gtx.Dp(8))
	}
	bubble.X = min(bubble.X, room)
	bubble.Y = min(bubble.Y, max(0, s.height-gtx.Dp(2)))
	x := gtx.Dp(rowInsetLeft) + s.slot
	switch {
	case s.service:
		bubble.X = min(bubble.X, gtx.Dp(serviceMaxWidth)/2)
		x = (size.X - bubble.X) / 2
	case s.outgoing:
		x = size.X - gtx.Dp(rowInsetRight) - s.slot - bubble.X
	}
	y := s.height - bubble.Y - gtx.Dp(4)
	radius := min(bubbleRadiusOf(gtx), bubble.Y/2)
	if s.service {
		radius = bubble.Y / 2
	}
	surface := scheme(gtx).Surface
	if s.outgoing {
		surface = scheme(gtx).PrimaryContainer
	}
	rect := image.Rectangle{Min: image.Pt(x, max(0, y)), Max: image.Pt(x+bubble.X, max(0, y)+bubble.Y)}
	paint.FillShape(gtx.Ops, surface.Color.SetOpacity(.5).AsNRGBA(), clip.UniformRRect(rect, radius).Op(gtx.Ops))
	if !s.service {
		p.drawBars(gtx, s, rect, surface.OnColor)
	}
	return layout.Dimensions{Size: size}
}
