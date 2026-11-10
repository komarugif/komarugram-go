// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"sync"
	"time"
)

// The pages of history Telegram is asked for are spaced so that a fast
// scroll stays under its limit of messages.getHistory, which stopped the
// history where it was with FLOOD_WAIT. Measured, it lets about 30 requests
// through in a sliding 30 s: the 31st in 19 s was refused, told to wait
// until 30 s after the first; 20 in any 10 s did not keep under it. The
// first few requests follow one another closely, as a scroll that stops
// soon needs; held on, the scroll waits longer between them, step by step,
// up to the rate at which the window holds no more than paceBudget, and no
// request ever makes it hold more. Telegram counts the account's requests
// across a restart of the client; the pacer, those of this run.
const (
	// paceWindow and paceBudget are the window the limit is taken over and
	// the requests it may hold: fewer than the 30 that passed.
	paceWindow = 30 * time.Second
	paceBudget = 25
	// paceFree requests in the window follow one another paceGap apart.
	paceFree = 4
	paceGap  = 150 * time.Millisecond
	// paceStep is how much the gap grows with each request after them, up
	// to the window's share for one request (paceWindow / paceBudget).
	paceStep = 100 * time.Millisecond
)

// historyPacer spaces the requests for history pages of an account. Each
// request takes its time under the lock, so that requests of several
// chats at once are spaced among themselves too.
type historyPacer struct {
	mu sync.Mutex
	// times are the moments of the requests in the window, the ones that
	// wait among them, in order.
	times []time.Time
	// now and sleep are time.Now and a sleep that ends with ctx; tests
	// replace them with a clock of their own.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// gap is the least time between a request and the one before it when the
// window holds n requests before it.
func paceGapFor(n int) time.Duration {
	if n < paceFree {
		return paceGap
	}
	return min(paceWindow/paceBudget, paceGap+time.Duration(n-paceFree+1)*paceStep)
}

// reserve takes the moment of the next request, at now or later.
func (p *historyPacer) reserve(now time.Time) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	drop := 0
	for drop < len(p.times) && !p.times[drop].After(now.Add(-paceWindow)) {
		drop++
	}
	p.times = append(p.times[:0], p.times[drop:]...)
	at := now
	if n := len(p.times); n > 0 {
		at = later(at, p.times[n-1].Add(paceGapFor(n)))
		if n >= paceBudget {
			// The window may not hold more: wait for its oldest to leave.
			at = later(at, p.times[n-paceBudget].Add(paceWindow))
		}
	}
	p.times = append(p.times, at)
	return at
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// wait waits for the moment of the next request, and returns how long it
// waited.
func (p *historyPacer) wait(ctx context.Context) (time.Duration, error) {
	now, sleep := time.Now, sleepCtx
	if p.now != nil {
		now = p.now
	}
	if p.sleep != nil {
		sleep = p.sleep
	}
	start := now()
	d := p.reserve(start).Sub(start)
	if d <= 0 {
		return 0, nil
	}
	return d, sleep(ctx, d)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// used is how many requests the window holds now, the ones waiting among
// them.
func (p *historyPacer) used() int {
	now := time.Now
	if p.now != nil {
		now = p.now
	}
	from := now().Add(-paceWindow)
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, at := range p.times {
		if at.After(from) {
			n++
		}
	}
	return n
}

// A page has pageNormal messages, as one asked alone, when a chat opens or
// the view reaches an end and stops; pageActive, getHistory's most, in a
// scroll that goes on, a page asked within pageActiveGap of the one before:
// half the requests, under the pacer's window, for the same scroll. Telegram
// Desktop asks for 50 (30 when a chat opens), and its export for 100.
const (
	pageNormal    = 50
	pageActive    = 100
	pageActiveGap = 2 * time.Second
)
