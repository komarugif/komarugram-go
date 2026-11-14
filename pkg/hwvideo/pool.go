// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"sync"
	"time"
)

// A session is costly to make and to let go (its surfaces, the driver's
// memory), and the list of saved GIFs ends and starts streams all the
// while it scrolls: a tile that leaves gives its session to one that comes
// in with pictures of the same size. The pool keeps a few sessions of
// streams that ended, for a while.

const (
	// poolSize bounds the sessions kept: a row or two of tiles.
	poolSize = 6
	// poolAge is how long a session is kept unused.
	poolAge = 5 * time.Second
)

// sessionShape is what a session was made for: a stream whose pictures
// are of its size, and that needs no more surfaces, references or a higher
// level, can be decoded in it.
type sessionShape struct {
	widthMbs, heightMbs int
	surfaces            int
	refs                uint8
	level               uint8
}

func shapeOf(sps *SPS, surfaces int) sessionShape {
	return sessionShape{widthMbs: sps.WidthMbs, heightMbs: sps.HeightMbs, surfaces: surfaces, refs: max(sps.MaxRefFrames, 1), level: sps.Level}
}

// fits reports whether a session of s decodes a stream of want.
func (s sessionShape) fits(want sessionShape) bool {
	return s.widthMbs == want.widthMbs && s.heightMbs == want.heightMbs &&
		s.surfaces >= want.surfaces && s.refs >= want.refs && s.level >= want.level
}

// pooledDevice is a device whose sessions go to its pool when closed.
type pooledDevice struct {
	device
	mu   sync.Mutex
	idle []*pooledSession
	// now is time.Now, but in tests.
	now func() time.Time
	// made and reused count the sessions made and those taken from the pool.
	made, reused int
}

func newPooledDevice(d device) *pooledDevice {
	return &pooledDevice{device: d, now: time.Now}
}

// pooledSession is a session of a pooledDevice.
type pooledSession struct {
	session
	d     *pooledDevice
	shape sessionShape
	// since is when it was put in the pool.
	since time.Time
}

func (d *pooledDevice) open(sps *SPS, surfaces int) (session, error) {
	want := shapeOf(sps, surfaces)
	d.mu.Lock()
	expired := d.expire()
	var found *pooledSession
	for i, s := range d.idle {
		if s.shape.fits(want) {
			found = s
			d.idle = append(d.idle[:i], d.idle[i+1:]...)
			d.reused++
			break
		}
	}
	d.mu.Unlock()
	closeAll(expired)
	if found != nil {
		found.reset()
		return found, nil
	}
	s, err := d.device.open(sps, surfaces)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.made++
	d.mu.Unlock()
	return &pooledSession{session: s, d: d, shape: want}, nil
}

// close puts the session in the pool, letting go of the oldest one there
// if it is full.
func (s *pooledSession) close() {
	d := s.d
	d.mu.Lock()
	s.since = d.now()
	d.idle = append(d.idle, s)
	expired := d.expire()
	if len(d.idle) > poolSize {
		expired = append(expired, d.idle[0])
		d.idle = d.idle[1:]
	}
	d.mu.Unlock()
	closeAll(expired)
}

// expire takes the sessions kept too long out of the pool; d.mu is held.
func (d *pooledDevice) expire() []*pooledSession {
	now := d.now()
	var old []*pooledSession
	kept := d.idle[:0]
	for _, s := range d.idle {
		if now.Sub(s.since) >= poolAge {
			old = append(old, s)
		} else {
			kept = append(kept, s)
		}
	}
	clear(d.idle[len(kept):])
	d.idle = kept
	return old
}

func closeAll(sessions []*pooledSession) {
	for _, s := range sessions {
		s.session.close()
	}
}

// close lets go of the sessions kept, then of the device.
func (d *pooledDevice) close() {
	d.mu.Lock()
	idle := d.idle
	d.idle = nil
	d.mu.Unlock()
	closeAll(idle)
	d.device.close()
}
