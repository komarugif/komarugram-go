// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"time"

	"gioui.org/layout"
	"gioui.org/op"
)

const (
	// fastScrollViews is how many heights of the view the history scrolls
	// in a second for the media in it to be held back: what passes that
	// fast is off screen in a moment, and a load started for it is a
	// download no one waits for.
	fastScrollViews = 1.5
	// paceRecheck is how soon after a frame that held media back another
	// is drawn: a list that stopped draws none of its own, and the speed
	// is only known from the frames.
	paceRecheck = 50 * time.Millisecond
)

// scrollPace is how fast the history scrolls, from the frames that laid it
// out.
type scrollPace struct {
	known bool
	// at is where the list was, in pixels from its top, and when.
	pos int64
	at  time.Time
	// speed is in pixels a second, and fast tells it is past what media
	// are held back at.
	speed float64
	fast  bool
}

// scrolled is how far the list is scrolled, in pixels from its top, by
// the heights it keeps; false when they are not known.
func (p *chatPage) scrolled() (int64, bool) {
	if p.heights == nil {
		return 0, false
	}
	pos := p.list.Position
	return p.heights.Prefix(pos.First) + int64(pos.Offset), true
}

// holdMedia has the media that come to the screen held back, with their
// rings, while the history scrolls fast; it is called before the list is
// laid out, and releaseMedia after.
func (p *chatPage) holdMedia() {
	if p.media != nil {
		p.media.SetDeferring(p.pace.fast)
	}
}

// releaseMedia ends holdMedia, and works out from where the list is the
// speed the next frame holds media back at. A frame that held some back
// is followed by another soon, which starts them once the list is slow or
// still.
func (p *chatPage) releaseMedia(gtx layout.Context, now time.Time) {
	if p.media == nil {
		return
	}
	p.media.SetDeferring(false)
	held := p.media.Deferred()
	pos, ok := p.scrolled()
	if !ok {
		p.pace = scrollPace{}
		return
	}
	pace := &p.pace
	pace.observe(pos, now, p.viewHeight)
	if held > 0 || pace.fast {
		gtx.Execute(op.InvalidateCmd{At: now.Add(paceRecheck)})
	}
}

// observe takes the list to be pos pixels from its top at now, with a view
// view pixels high.
func (s *scrollPace) observe(pos int64, now time.Time, view int) {
	if s.known {
		if dt := now.Sub(s.at); dt > 0 {
			moved := pos - s.pos
			if moved < 0 {
				moved = -moved
			}
			// Slowing down is taken at half a frame at a time, for the media
			// not to start and stop with the wheel's bursts.
			s.speed = max(float64(moved)/dt.Seconds(), s.speed/2)
			s.fast = s.speed > fastScrollViews*float64(view)
		}
	}
	s.known, s.pos, s.at = true, pos, now
}
