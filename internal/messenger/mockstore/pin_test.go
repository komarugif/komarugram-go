// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// The demo keeps the order its pinned chats are dragged to, and refuses one
// that is not of all of them.
func TestDemoReordersPinned(t *testing.T) {
	s := New(time.Now(), 0)
	ctx := context.Background()
	// The demo pins one chat: two more are pinned as the menu does.
	for _, c := range s.Chats()[1:3] {
		if err := s.PinChat(ctx, c.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	order := model.PinnedOrder(s.Chats())
	if len(order) < 3 {
		t.Fatalf("the demo pins %v, too few to reorder", order)
	}
	slices.Reverse(order)
	if err := s.ReorderPinned(ctx, order); err != nil {
		t.Fatal(err)
	}
	if got := model.PinnedOrder(s.Chats()); !slices.Equal(got, order) {
		t.Fatalf("pinned %v, want %v", got, order)
	}
	for i, c := range s.Chats()[:len(order)] {
		if c.ID != order[i] {
			t.Fatalf("the list starts with %d, want the pinned in their order %v", c.ID, order)
		}
	}
	if err := s.ReorderPinned(ctx, order[1:]); !errors.Is(err, model.ErrPinnedChanged) {
		t.Fatalf("an order without a pinned chat: %v", err)
	}
}
