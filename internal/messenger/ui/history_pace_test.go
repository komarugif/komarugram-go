// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"
	"time"
)

// Media are held back while the list moves more than a view and a half a
// second, and once it is slow or still they are not.
func TestScrollPace(t *testing.T) {
	var s scrollPace
	now := time.Now()
	const view = 800
	s.observe(0, now, view)
	if s.fast {
		t.Fatal("fast at the first frame")
	}
	// Reading: 400 px a second.
	now = now.Add(time.Second / 60)
	s.observe(7, now, view)
	if s.fast {
		t.Fatal("fast while reading")
	}
	// Flying: 3000 px a second.
	now = now.Add(time.Second / 60)
	s.observe(57, now, view)
	if !s.fast {
		t.Fatal("not fast at 3000 px a second")
	}
	// Stopped: a frame a moment later at the same place, twice.
	for range 2 {
		now = now.Add(paceRecheck)
		s.observe(57, now, view)
	}
	if s.fast {
		t.Fatal("still fast once stopped")
	}
}
