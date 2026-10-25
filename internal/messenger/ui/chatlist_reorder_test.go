// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"komarugram/internal/messenger/model"
)

// ReorderPinned records the order the list saves.
func (s *pinStore) ReorderPinned(_ context.Context, order []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.orders = append(s.orders, slices.Clone(order))
	return nil
}

func (s *pinStore) savedOrders() [][]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.orders)
}

func newReorderHarness(t *testing.T, pinned int) *listHarness {
	h := newListHarness(t, testChats(pinned))
	h.list.reorder.store = h.store
	h.frames(2)
	return h
}

func (h *listHarness) pointer(kind pointer.Kind, pos f32.Point) {
	h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Time: time.Duration(h.now.UnixNano())})
	h.frames(1)
}

// drag presses at from and goes to to in steps, without letting go.
func (h *listHarness) drag(from, to f32.Point) {
	h.pointer(pointer.Press, from)
	const steps = 8
	for i := 1; i <= steps; i++ {
		f := float32(i) / steps
		h.pointer(pointer.Move, f32.Pt(from.X+(to.X-from.X)*f, from.Y+(to.Y-from.Y)*f))
	}
}

// waitSaved draws frames until the list has saved n orders.
func (h *listHarness) waitSaved(n int) [][]int64 {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(h.store.savedOrders()) < n && time.Now().Before(deadline) {
		h.frames(1)
		time.Sleep(time.Millisecond)
	}
	return h.store.savedOrders()
}

func shownOrder(h *listHarness) []int64 {
	return model.PinnedOrder(h.list.reorder.arrange(h.chats))
}

// TestReorderPinnedDrag drags the first pinned chat below the third: the
// others slide up out of its way while it is held, and when it is let go
// the new order is saved, and the chat it was let go over is not opened.
func TestReorderPinnedDrag(t *testing.T) {
	h := newReorderHarness(t, 4)
	h.drag(rowAt(0), rowAt(2))
	if !h.list.reorder.dragging || h.list.reorder.lifted != 1 {
		t.Fatalf("dragging %v chat %d, want chat 1 lifted", h.list.reorder.dragging, h.list.reorder.lifted)
	}
	if got := shownOrder(h); !slices.Equal(got, []int64{2, 3, 1, 4}) {
		t.Fatalf("the list shows the pinned as %v while chat 1 is dragged two rows down", got)
	}
	if off := h.list.reorder.offset(3, h.now); off <= 0 {
		t.Fatalf("chat 3, just passed, is at %v from its place: it should slide up into it", off)
	}
	h.pointer(pointer.Release, rowAt(2))
	if got := h.waitSaved(1); len(got) != 1 || !slices.Equal(got[0], []int64{2, 3, 1, 4}) {
		t.Fatalf("saved %v, want [2 3 1 4]", got)
	}
	// The store has the order now.
	h.chats = model.WithPinOrder(h.chats, []int64{2, 3, 1, 4})
	h.frames(30)
	if len(h.picks) != 0 {
		t.Fatalf("picked %v by a drag", h.picks)
	}
	if h.list.reorder.lifted != 0 || h.list.reorder.order != nil {
		t.Fatalf("lifted %d, order %v after the row slid into its place", h.list.reorder.lifted, h.list.reorder.order)
	}
}

// TestReorderPinnedClick checks that a click on a pinned chat opens it, and
// moves nothing.
func TestReorderPinnedClick(t *testing.T) {
	h := newReorderHarness(t, 3)
	h.press(pointer.ButtonPrimary, rowAt(1))
	h.frames(5)
	if !slices.Equal(h.picks, []int64{2}) {
		t.Fatalf("picked %v, want chat 2", h.picks)
	}
	if h.list.reorder.lifted != 0 || len(h.store.savedOrders()) != 0 {
		t.Fatal("a click lifted the row")
	}
}

// TestReorderPinnedHold lifts a pinned chat held in place; let go where it
// was, it changes nothing and opens nothing.
func TestReorderPinnedHold(t *testing.T) {
	h := newReorderHarness(t, 3)
	h.pointer(pointer.Press, rowAt(1))
	h.frames(int(reorderHold/(16*time.Millisecond)) + 2)
	if !h.list.reorder.dragging || h.list.reorder.lifted != 2 {
		t.Fatalf("held: dragging %v chat %d", h.list.reorder.dragging, h.list.reorder.lifted)
	}
	h.pointer(pointer.Release, rowAt(1))
	h.frames(30)
	if len(h.picks) != 0 || len(h.store.savedOrders()) != 0 {
		t.Fatalf("picked %v, saved %v by a hold", h.picks, h.store.savedOrders())
	}
}

// TestReorderOnlyPinned checks that a drag of a chat that is not pinned, or
// of a pinned one in a folder, moves nothing.
func TestReorderOnlyPinned(t *testing.T) {
	h := newReorderHarness(t, 3)
	h.drag(rowAt(5), rowAt(0))
	h.pointer(pointer.Release, rowAt(0))
	if h.list.reorder.lifted != 0 || len(h.store.savedOrders()) != 0 {
		t.Fatal("an unpinned chat was dragged")
	}

	h = newReorderHarness(t, 3)
	h.sec = section{kind: sectionFolder, folder: 1}
	h.frames(2)
	h.drag(rowAt(0), rowAt(2))
	if h.list.reorder.lifted != 0 {
		t.Fatal("a pinned chat was dragged in a folder")
	}
}

// TestReorderPinnedRefused checks that an order the store refuses goes, and
// the toast tells so.
func TestReorderPinnedRefused(t *testing.T) {
	h := newReorderHarness(t, 3)
	h.store.fail = errors.New("boom")
	h.drag(rowAt(2), rowAt(0))
	h.pointer(pointer.Release, rowAt(0))
	deadline := time.Now().Add(2 * time.Second)
	for h.list.toast.Text() == "" && time.Now().Before(deadline) {
		h.frames(1)
		time.Sleep(time.Millisecond)
	}
	if want := "Could not change the pinned chats"; h.list.toast.Text() != want {
		t.Fatalf("toast %q, want %q", h.list.toast.Text(), want)
	}
	h.frames(2)
	if got := shownOrder(h); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("the list shows %v after the order was refused", got)
	}
}

// TestReorderPinnedChangedMeanwhile checks that a pin while a chat is
// dragged ends the drag, and saves nothing.
func TestReorderPinnedChangedMeanwhile(t *testing.T) {
	h := newReorderHarness(t, 3)
	h.drag(rowAt(0), rowAt(2))
	h.chats = model.WithPinned(h.chats, 5, true)
	h.frames(1)
	h.pointer(pointer.Release, rowAt(2))
	h.frames(5)
	if h.list.reorder.dragging || len(h.store.savedOrders()) != 0 {
		t.Fatalf("dragging %v, saved %v after a pin meanwhile", h.list.reorder.dragging, h.store.savedOrders())
	}
}

// idle lets d pass as a window with nothing else going on does: a frame is
// drawn only when the list asked for one, at the time it asked.
func (h *listHarness) idle(d time.Duration) (frames int) {
	end := h.now.Add(d)
	for {
		// The window keeps the time a frame asked for, as setNextFrame.
		if at, ok := h.router.WakeupTime(); ok && (!h.wakes || at.Before(h.wake)) {
			h.wake, h.wakes = at, true
		}
		if !h.wakes || h.wake.After(end) {
			h.now = end
			return frames
		}
		if h.wake.After(h.now) {
			h.now = h.wake
		}
		h.wakes = false
		h.frames(1)
		frames++
		if frames > 1000 {
			h.t.Fatal("the list asks for frames for ever")
		}
	}
}

// quietPointer sends one pointer event, as a window does: one frame for
// it, and then only the frames asked for.
func (h *listHarness) quietPointer(kind pointer.Kind, pos f32.Point) {
	h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Time: time.Duration(h.now.UnixNano())})
	h.frames(1)
	h.idle(0)
}

// TestReorderPinnedInAQuietWindow presses, holds, drags and lets go as a
// window with nothing else animating gets them: the row lifts on the hold
// alone, lands where it is let go even when no move came between, and is
// set down when its slide is over, with no event to draw the frames.
func TestReorderPinnedInAQuietWindow(t *testing.T) {
	for _, moves := range []bool{true, false} {
		h := newReorderHarness(t, 3)
		h.idle(time.Second)
		h.quietPointer(pointer.Press, rowAt(2))
		h.idle(reorderHold + 100*time.Millisecond)
		if !h.list.reorder.dragging || h.list.reorder.lifted != 3 {
			t.Fatalf("moves %v: held in a quiet window, dragging %v chat %d", moves, h.list.reorder.dragging, h.list.reorder.lifted)
		}
		if moves {
			for y := rowAt(2).Y; y > rowAt(0).Y; y -= 10 {
				h.quietPointer(pointer.Move, f32.Pt(rowAt(0).X, y))
				h.idle(100 * time.Millisecond)
			}
		}
		h.quietPointer(pointer.Release, rowAt(0))
		// The save is answered without a frame: the harness has no window
		// to invalidate.
		deadline := time.Now().Add(2 * time.Second)
		for len(h.store.savedOrders()) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got := h.store.savedOrders(); len(got) != 1 || !slices.Equal(got[0], []int64{3, 1, 2}) {
			t.Fatalf("moves %v: saved %v, want [3 1 2]", moves, got)
		}
		h.chats = model.WithPinOrder(h.chats, []int64{3, 1, 2})
		h.idle(time.Second)
		if r := &h.list.reorder; r.lifted != 0 || r.drawn != 0 {
			t.Fatalf("moves %v: a second after chat 3 was let go, chat %d is lifted, and %d drawn so", moves, r.lifted, r.drawn)
		}
		if len(h.picks) != 0 {
			t.Fatalf("moves %v: picked %v", moves, h.picks)
		}
	}
}
