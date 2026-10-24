// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

// The updates manager starts asking for no channel's difference: it asked
// for every channel the account ever had an update of, on every start, and
// an account with many met FLOOD_WAIT. A channel starts when an update of it
// comes, from the pts the dialogs keep, newer than where it stood before.
func TestUpdatesStartNoChannel(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const self = 1
	cache := s.Cache()
	if err := cache.SetState(ctx, self, updates.State{Pts: 10, Date: 1, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []int64{7, 8, 9} {
		if err := cache.SetChannelPts(ctx, self, ch, 100); err != nil {
			t.Fatal(err)
		}
		if err := cache.SetChannelAccessHash(ctx, self, ch, ch*11); err != nil {
			t.Fatal(err)
		}
	}
	// The dialogs tell channel 7 went on; 8 is older than kept, and 12 is
	// not kept.
	if err := s.keepChannelPts(ctx, self, map[int64]int{7: 150, 8: 90, 12: 40}); err != nil {
		t.Fatal(err)
	}
	for ch, want := range map[int64]int{7: 150, 8: 100} {
		if got, _, _ := cache.GetChannelPts(ctx, self, ch); got != want {
			t.Fatalf("channel %d kept at %d, want %d", ch, got, want)
		}
	}
	if _, found, _ := cache.GetChannelPts(ctx, self, 12); found {
		t.Fatal("a channel not kept was kept")
	}

	var mu sync.Mutex
	var asked []int // the pts each channel difference was asked from
	api := tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		switch r := in.(type) {
		case *tg.UpdatesGetDifferenceRequest:
			out.(*tg.UpdatesDifferenceBox).Difference = &tg.UpdatesDifferenceEmpty{Date: 1, Seq: 1}
		case *tg.UpdatesGetChannelDifferenceRequest:
			mu.Lock()
			asked = append(asked, r.Pts)
			mu.Unlock()
			out.(*tg.UpdatesChannelDifferenceBox).ChannelDifference = &tg.UpdatesChannelDifferenceEmpty{Pts: r.Pts, Final: true}
		case *tg.UpdatesGetStateRequest:
			*out.(*tg.UpdatesState) = tg.UpdatesState{Pts: 10, Date: 1, Seq: 1}
		}
		return nil
	}))
	m := s.Updates()
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = m.Run(run, api, self, updates.AuthOptions{})
	}()
	t.Cleanup(func() { stop(); <-done })
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	n := len(asked)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("the start asked for %d channel differences, want none", n)
	}

	err := m.Handle(ctx, &tg.Updates{
		Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 5, PeerID: &tg.PeerChannel{ChannelID: 7}, Date: 2}, Pts: 151, PtsCount: 1}},
		Chats:   []tg.ChatClass{&tg.Channel{ID: 7, AccessHash: 77, Title: "seven"}},
		Date:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		got := append([]int(nil), asked...)
		mu.Unlock()
		if len(got) > 0 {
			if got[0] != 150 {
				t.Fatalf("channel 7 started from %d, want 150, what the dialogs told", got[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an update of a channel did not start it")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
