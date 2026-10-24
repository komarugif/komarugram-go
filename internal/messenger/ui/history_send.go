// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"komarugram/internal/messenger/model"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// sentNoteLife is how long the composer's note of a message sent waits for
// the message to show in the history: a send that failed leaves its note.
const sentNoteLife = 20 * time.Second

// sentNote is what the composer keeps of a message it sent, for the history
// to fly it up from the composer once it shows there.
type sentNote struct {
	chat int64
	at   time.Time
}

// noteSent notes that a message was sent to chat.
func (c *messageComposer) noteSent(chat int64) {
	c.sentNotes = append(c.sentNotes, sentNote{chat, time.Now()})
}

// forgetSent drops the note of a message to chat that was not sent.
func (c *messageComposer) forgetSent(chat int64) {
	for i, n := range c.sentNotes {
		if n.chat == chat {
			c.sentNotes = append(c.sentNotes[:i], c.sentNotes[i+1:]...)
			return
		}
	}
}

// takeSent reports whether a message was sent to chat, and drops its note.
func (c *messageComposer) takeSent(chat int64) bool {
	live := c.sentNotes[:0]
	for _, n := range c.sentNotes {
		if time.Since(n.at) < sentNoteLife {
			live = append(live, n)
		}
	}
	c.sentNotes = live
	for i, n := range c.sentNotes {
		if n.chat == chat {
			c.sentNotes = append(c.sentNotes[:i], c.sentNotes[i+1:]...)
			return true
		}
	}
	return false
}

// sendFlight is the flight of a message just sent, from the composer to its
// place in the history, or of one that came, which rises a little to its
// place: arrival.
type sendFlight struct {
	tween   wdk.FloatTween
	arrival bool
}

// arrivalRise is how far below its place a message that came starts.
const arrivalRise = unit.Dp(24)

// arrivalAge is how old a message that comes at the end of the history may
// be and still rise to its place: older ones come with pages loaded, not as
// they are sent.
const arrivalAge = 2 * time.Minute

// newFlight starts a flight, at the composer or below the place.
func newFlight(gtx layout.Context, arrival bool) *sendFlight {
	f := &sendFlight{arrival: arrival}
	f.tween.Duration = token.DurationMedium2
	f.tween.Easing = &token.EasingEmphasizedDecelerate
	f.tween.Animate(gtx, 0) // starts at the start
	return f
}

// sentByUser reports whether the message is one that the user's own composer
// sent: an outgoing one, or any in Saved Messages, where Telegram does not
// mark the user's messages as outgoing.
func (p *chatPage) sentByUser(m model.Message) bool {
	return m.Outgoing || p.kind == model.KindSaved
}

// startFlights sets flying the messages that came at the end of the history
// after prev, the messages shown before: from the composer, one for each
// message it sent, and the others, of other users and of the account's
// other devices, rising to their places. A message on its way flies; the
// message Telegram makes of it, which takes its place, flies on from where
// it is. It does nothing with animations off, or when prev did not show the
// history's end.
func (p *chatPage) startFlights(gtx layout.Context, prev []model.Message, animate bool) {
	if !animate || !wdk.AnimationsEnabled(gtx) || p.composer == nil || len(prev) == 0 {
		return
	}
	var prevLast model.MessageID
	was := map[model.MessageID]bool{}
	for _, m := range prev {
		if m.Local() {
			was[m.Key.MessageID] = true
		} else {
			prevLast = m.Key.MessageID
		}
	}
	// The user's messages on their way that are gone, oldest first: the
	// messages Telegram made of them took their places, and go on with
	// their flights, or stand where they are once those ended. waiting
	// counts those still there, which a message of Telegram's may come
	// before.
	var gone []model.MessageID
	waiting := 0
	for _, m := range prev {
		if m.Sending && p.sentByUser(m) {
			if p.hasMessage(m.Key.MessageID) {
				waiting++
			} else {
				gone = append(gone, m.Key.MessageID)
			}
		}
	}
	defer func() {
		for id := range p.flights {
			if id < 0 && !p.hasMessage(id) {
				delete(p.flights, id)
			}
		}
	}()
	for _, m := range p.messages {
		// What was on its way and is still there, though taken, as the
		// demo keeps it, came before.
		fresh := !was[m.Key.MessageID] && (m.Sending || !m.Local() && m.Key.MessageID > prevLast)
		if !fresh {
			continue
		}
		mine := p.sentByUser(m)
		var f *sendFlight
		switch {
		case mine && !m.Sending && len(gone) > 0:
			// The flight of the message it was; none, once it landed.
			f = p.flights[gone[0]]
			gone = gone[1:]
			if f == nil {
				continue
			}
		case mine && !m.Sending && waiting > 0:
			// It came before the message on its way went: it is where that
			// one stands.
			waiting--
			continue
		case mine && p.composer.takeSent(p.chat):
			f = newFlight(gtx, false)
		case gtx.Now.Sub(m.Date) < arrivalAge:
			f = newFlight(gtx, true)
		default:
			continue
		}
		if p.flights == nil {
			p.flights = map[model.MessageID]*sendFlight{}
		}
		p.flights[m.Key.MessageID] = f
	}
}

// flyingRow draws row i with draw, a message that flies from the composer,
// or came, if it does: it rises to its place while it fades in. composer is
// where the composer's middle is, down from the top of the history's view,
// and end the room after the row, the history's tail after its last, in
// px; a message that came rises arrivalRise.
func (p *chatPage) flyingRow(gtx layout.Context, i, composer, end int, draw func(layout.Context) layout.Dimensions) layout.Dimensions {
	f := p.flights[p.messages[i].Key.MessageID]
	if f == nil {
		return draw(gtx)
	}
	record := op.Record(gtx.Ops)
	dims := draw(gtx)
	call := record.Stop()
	t := f.tween.Animate(gtx, 1)
	if t >= 1 {
		delete(p.flights, p.messages[i].Key.MessageID)
		call.Add(gtx.Ops)
		return dims
	}
	rise := gtx.Dp(arrivalRise)
	if !f.arrival {
		rise = composer - (p.endTop(i, dims.Size.Y+end) + dims.Size.Y/2)
	}
	shift := int(float32(rise) * (1 - t))
	defer op.Offset(image.Pt(0, shift)).Push(gtx.Ops).Pop()
	defer paint.PushOpacity(gtx.Ops, t).Pop()
	call.Add(gtx.Ops)
	return dims
}

// endTop is where row i, h high, lies down from the top of the history's
// view at its end: the rows end at the view's bottom, or, too few to fill
// it, begin at its top, as a short history, a new Saved Messages', does.
// Its own height is h, which its measure may not be yet.
func (p *chatPage) endTop(i, h int) int {
	if p.heights == nil || p.list.Position.BeforeEnd {
		// As the last row at the bottom of the view.
		return p.viewHeight - h
	}
	before := int(p.heights.Prefix(i))
	after := int(p.heights.Total() - p.heights.Prefix(i+1))
	return before - max(0, before+h+after-p.viewHeight)
}
