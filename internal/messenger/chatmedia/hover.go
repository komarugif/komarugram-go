// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"image"
	"time"

	"komarugram/internal/messenger/model"
)

// HoverPlayback describes playback for one visible copy of a media item.
// Target is a stable, comparable identity of the view. A nil target
// preserves autoplay without tracking a hover. VideoLoop also enables
// finishing for video stickers used as gifts.
type HoverPlayback struct {
	Target              any
	Autoplay, Hovered   bool
	FullRate, VideoLoop bool
	playing             bool
}

type hoverLoop struct {
	entry *entry
	// last is the last absolute frame of the cycle the pointer saw.
	last     int64
	seen     uint64
	finished bool
}

// StatusHover finishes the current Lottie loop after the pointer leaves and
// keeps its last picture visible until the next hover. Other copies of the
// media keep their own view state.
func (m *Manager) StatusHover(msg model.Message, want image.Point, cover bool, hover HoverPlayback) Status {
	return m.status(msg, hover.Autoplay || hover.Hovered, want, cover, hover.FullRate, &hover)
}

// hoverFrame runs with e.mu held, after accepting the worker's due frame.
func (m *Manager) hoverFrame(e *entry, hover HoverPlayback, hold bool) (play bool, finished image.Image) {
	play = hover.Autoplay || hover.Hovered
	if hover.Target == nil {
		return hover.Autoplay, nil
	}
	run, seen := m.hoverLoops[hover.Target]
	seen = seen && run.entry == e
	if hover.Autoplay || e.loopFrames <= 1 || (!e.lottieLoop && !hover.VideoLoop) {
		delete(m.hoverLoops, hover.Target)
		return play, nil
	}
	if !hold && hover.Hovered && e.still != nil {
		e.wantLoopEnd.Store(true)
		run = hoverLoop{entry: e, last: (e.frameNumber/e.loopFrames+1)*e.loopFrames - 1}
		seen = true
	}
	if !seen {
		delete(m.hoverLoops, hover.Target)
		return play, nil
	}
	if m.hoverLoops == nil {
		m.hoverLoops = make(map[any]hoverLoop)
	}
	run.seen = m.generation
	if !hover.Hovered && e.frameNumber >= run.last {
		run.finished = true
	}
	m.hoverLoops[hover.Target] = run
	if !run.finished {
		return true, nil
	}
	if e.loopEnd == nil && e.frameNumber == run.last && e.frame != nil {
		e.loopEnd = toRGBA(e.frame)
	}
	return false, e.loopEnd
}

// prepareLoopEnd keeps one immutable image per animated media that has a
// hovered view. It stays valid if another view keeps playing the shared
// worker's later frames or the decoder reuses its pixel buffers.
func prepareLoopEnd(e *entry, frame func(time.Duration) (image.Image, error), step time.Duration, index int64) bool {
	e.mu.Lock()
	last := e.loopFrames - 1
	ready := e.loopEnd != nil
	prepare := e.lottieLoop || e.clip != nil || index >= last
	e.mu.Unlock()
	if ready || !prepare {
		return ready
	}
	im, err := frame(time.Duration(last)*step + step/2)
	var end image.Image
	if err == nil && nilImage(im) != nil {
		end = toRGBA(im)
	}
	e.mu.Lock()
	if e.loopEnd == nil {
		e.loopEnd = end
	}
	e.mu.Unlock()
	return true
}
