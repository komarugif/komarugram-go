// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows || darwin

package appearance

import (
	"image/color"
	"time"
)

const pollInterval = 5 * time.Second

// poll calls read, and readAccent when not nil, now and every pollInterval
// until the returned function is called.
func poll(m *Monitor, read func() Scheme, readAccent func() (color.NRGBA, bool)) func() {
	update := func() {
		m.set(read())
		if readAccent != nil {
			m.setAccent(readAccent())
		}
	}
	update()
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				update()
			}
		}
	}()
	var once bool
	return func() {
		if !once {
			once = true
			close(done)
		}
	}
}
