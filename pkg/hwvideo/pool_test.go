// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"image"
	"testing"
	"time"

	"komarugram/pkg/resample"
)

// countingDevice makes sessions that count what is done to them.
type countingDevice struct {
	opened, closed int
	sessions       []*countingSession
}

type countingSession struct {
	d             *countingDevice
	resets        int
	closed        bool
	width, height int
}

func (d *countingDevice) open(sps *SPS, surfaces int) (session, error) {
	d.opened++
	s := &countingSession{d: d, width: sps.WidthMbs, height: sps.HeightMbs}
	d.sessions = append(d.sessions, s)
	return s, nil
}

func (d *countingDevice) close() {}

func (s *countingSession) decode(p *picture) error { return nil }
func (s *countingSession) read(surface int, sps *SPS, dst *image.YCbCr) error {
	return nil
}
func (s *countingSession) render(surface int, sps *SPS, plan resample.Plan, dst *image.RGBA) error {
	return noRender("a counting session")
}
func (s *countingSession) reset() { s.resets++ }
func (s *countingSession) close() {
	s.closed = true
	s.d.closed++
}

// TestPoolReusesSessions checks a stream is given the session of one that
// ended when it fits, made ready for it, and that the pool lets go of the
// sessions it keeps when full, after a while, and with the device.
func TestPoolReusesSessions(t *testing.T) {
	inner := new(countingDevice)
	d := newPooledDevice(inner)
	now := time.Unix(0, 0)
	d.now = func() time.Time { return now }
	sps := func(w, h int, refs, level uint8) *SPS {
		return &SPS{WidthMbs: w, HeightMbs: h, MaxRefFrames: refs, Level: level}
	}

	a, _ := d.open(sps(45, 80, 4, 31), 12)
	a.close()
	if inner.closed != 0 {
		t.Fatal("a session that ended was let go")
	}
	// Smaller in everything but the size: it fits.
	b, _ := d.open(sps(45, 80, 2, 30), 10)
	if inner.opened != 1 || b.(*pooledSession).session != inner.sessions[0] || inner.sessions[0].resets != 1 {
		t.Fatalf("not reused: %d opened, %d resets", inner.opened, inner.sessions[0].resets)
	}
	b.close()
	for _, want := range []*SPS{sps(45, 81, 2, 30), sps(45, 80, 5, 30), sps(45, 80, 2, 40)} {
		s, _ := d.open(want, 10)
		if s.(*pooledSession).session == inner.sessions[0] {
			t.Fatalf("a session given to a stream it does not fit: %+v", want)
		}
		defer s.close()
	}
	if s, _ := d.open(sps(45, 80, 4, 31), 13); s.(*pooledSession).session == inner.sessions[0] {
		t.Fatal("a session given to a stream of more surfaces")
	}

	// Full: the oldest goes.
	var many []session
	for i := range poolSize + 2 {
		s, _ := d.open(sps(10+i, 10, 1, 30), 4)
		many = append(many, s)
	}
	closedBefore := inner.closed
	for _, s := range many {
		s.close()
	}
	if got := inner.closed - closedBefore; got < 2 {
		t.Fatalf("%d let go of %d over a pool of %d", got, len(many), poolSize)
	}

	// Old ones go at the next open.
	now = now.Add(poolAge)
	d.open(sps(99, 99, 1, 30), 4)
	d.mu.Lock()
	idle := len(d.idle)
	d.mu.Unlock()
	if idle != 0 {
		t.Fatalf("%d sessions kept past their age", idle)
	}
	kept, _ := d.open(sps(98, 98, 1, 30), 4)
	kept.close()
	d.close()
	if !kept.(*pooledSession).session.(*countingSession).closed {
		t.Fatal("a session kept outlived the device")
	}
}
