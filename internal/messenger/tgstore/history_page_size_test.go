// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// A page asked alone, as when a chat opens or the view reaches an end once,
// has pageNormal messages; in a scroll that goes on, each asked soon after
// the one before, pageActive: half the requests for the same scroll, under
// the pacer's window. All were 80.
func TestPageSizeFollowsTheScroll(t *testing.T) {
	s := New(nil)
	if e := s.Configure(context.Background(), "a", filepath.Join(t.TempDir(), "h"), nil); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	chat := peerID(&tg.PeerUser{UserID: 5})
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.top[chat] = 5000
	history := serveHistory(1, 5000)
	var mu sync.Mutex
	var limits []int
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if req, ok := in.(*tg.MessagesGetHistoryRequest); ok {
			mu.Lock()
			limits = append(limits, req.Limit)
			mu.Unlock()
		}
		return history(ctx, in, out)
	}))
	s.OpenChat(chat)
	waitHistory(t, s, chat)
	for range 3 {
		s.LoadOlder(chat)
		waitHistory(t, s, chat)
	}
	time.Sleep(pageActiveGap + 100*time.Millisecond)
	s.LoadOlder(chat)
	waitHistory(t, s, chat)
	mu.Lock()
	defer mu.Unlock()
	want := []int{pageNormal, pageNormal, pageActive, pageActive, pageNormal}
	if !slices.Equal(limits, want) {
		t.Fatalf("pages of %v, want %v: the open, one alone, two of a scroll, one after a pause", limits, want)
	}
	if h := s.History(chat); len(h.Messages) != pageNormal*3+pageActive*2 || !h.HasOlder {
		t.Fatalf("%d messages loaded, older %v", len(h.Messages), h.HasOlder)
	}
}
