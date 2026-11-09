// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// A scroll held through the history asks Telegram for its pages spaced: the
// first close together, then further apart, step by step, and never more
// than paceBudget in a window of paceWindow. Asked for as fast as the view
// scrolled, the 31st in 19 seconds was refused with FLOOD_WAIT. Let go,
// the scroll asks again at once.
func TestFastScrollIsPaced(t *testing.T) {
	s := testStore(t)
	chat := peerID(&tg.PeerUser{UserID: 5})
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.top[chat] = 5000

	var mu sync.Mutex
	clock := time.Unix(1000, 0)
	var asked []time.Time
	s.history.pace.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	s.history.pace.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(d)
		return nil
	}
	history := serveHistory(1, 5000)
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetHistoryRequest); ok {
			mu.Lock()
			asked = append(asked, clock)
			mu.Unlock()
		}
		return history(ctx, in, out)
	}))

	s.OpenChat(chat)
	waitHistory(t, s, chat)
	for range 40 {
		s.LoadOlder(chat)
		waitHistory(t, s, chat)
	}
	mu.Lock()
	times := append([]time.Time(nil), asked...)
	mu.Unlock()
	if len(times) != 41 {
		t.Fatalf("%d pages asked for, want 41", len(times))
	}
	for i := range times {
		n := 0
		for _, at := range times[i:] {
			if at.Sub(times[i]) < paceWindow {
				n++
			}
		}
		if n > paceBudget {
			t.Fatalf("%d pages asked for in %s from the %dth: %v", n, paceWindow, i, gaps(times))
		}
	}
	g := gaps(times)
	for i, d := range g[:paceFree-1] {
		if d > paceGap {
			t.Fatalf("the %dth page waited %s, more than %s: %v", i+1, d, paceGap, g)
		}
	}
	for i := 1; i < paceBudget-1; i++ {
		if g[i] < g[i-1] {
			t.Fatalf("the gaps shrink before the window is full: %v", g)
		}
	}

	// Let go for a window, the next page is asked for at once.
	mu.Lock()
	clock = clock.Add(paceWindow)
	before := clock
	mu.Unlock()
	s.LoadOlder(chat)
	waitHistory(t, s, chat)
	mu.Lock()
	last := asked[len(asked)-1]
	mu.Unlock()
	if !last.Equal(before) {
		t.Fatalf("after a pause the page waited %s", last.Sub(before))
	}
}

func gaps(times []time.Time) []time.Duration {
	var out []time.Duration
	for i := 1; i < len(times); i++ {
		out = append(out, times[i].Sub(times[i-1]))
	}
	return out
}
