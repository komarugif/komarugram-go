// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"sync"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// pinStore records what the list's menu asks.
type pinStore struct {
	mu      sync.Mutex
	pinned  []int64
	read    []int64
	fail    error
	premium model.Premium
	// orders are the orders of the pinned chats saved.
	orders [][]int64
}

func (s *pinStore) PinChat(_ context.Context, chat int64, pin bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	if pin {
		s.pinned = append(s.pinned, chat)
	} else {
		s.pinned = append(s.pinned, -chat)
	}
	return nil
}
func (s *pinStore) MarkChatRead(chat int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.read = append(s.read, chat)
}
func (s *pinStore) Premium() model.Premium { return s.premium }
func (s *pinStore) asked() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.pinned...)
}

// listHarness draws the chat list on a router of its own.
type listHarness struct {
	t      *testing.T
	router input.Router
	list   *chatList
	store  *pinStore
	chats  []model.Chat
	sec    section
	now    time.Time
	l      localization.Catalog
	// picks are the chats picked in the list.
	picks []int64
	// wake is when the list asked for its next frame, if wakes.
	wake  time.Time
	wakes bool
}

func newListHarness(t *testing.T, chats []model.Chat) *listHarness {
	t.Helper()
	h := &listHarness{t: t, list: newChatList(), store: &pinStore{}, chats: chats, now: time.Unix(100000, 0), l: localization.For("en")}
	h.store.premium = model.PremiumFromConfig(false, nil)
	h.list.menu.store, h.list.menu.premium = h.store, h.store
	h.frames(2)
	return h
}

func (h *listHarness) frames(n int) {
	for range n {
		gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(320, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		if p, ok := h.list.Update(gtx, h.sec, h.l); ok {
			h.picks = append(h.picks, p.ID)
		}
		h.list.Layout(gtx, h.sec, nil, h.chats, 0, false, h.l)
		h.router.Frame(gtx.Ops)
		h.now = h.now.Add(16 * time.Millisecond)
	}
}

func (h *listHarness) press(button pointer.Buttons, pos f32.Point) {
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: button, Position: pos, Time: time.Duration(h.now.UnixNano())})
		h.frames(1)
	}
}

// rowAt is a point on the row of the chat at index i.
func rowAt(i int) f32.Point {
	return f32.Pt(150, float32(6+64*i+32))
}

// rightClick opens the menu of the chat at index i and returns where its
// first item is.
func (h *listHarness) rightClick(i int) f32.Point {
	h.t.Helper()
	at := rowAt(i)
	h.press(pointer.ButtonSecondary, at)
	h.frames(30)
	if !h.list.menu.open {
		h.t.Fatalf("the menu of chat %d did not open", i)
	}
	return f32.Pt(at.X+40, at.Y+float32(menuPadding)+float32(menuItemHeight)/2)
}

func testChats(pinned int) []model.Chat {
	var chats []model.Chat
	for i := 1; i <= 12; i++ {
		c := model.Chat{ID: int64(i), Kind: model.KindUser, Title: "Chat", LastTime: time.Unix(1000, 0)}
		if i <= pinned {
			c.Pinned, c.PinRank = true, i
		}
		chats = append(chats, c)
	}
	return chats
}

// TestChatRowMenuPins right-clicks a chat, and pins it from the menu; a
// pinned chat offers to unpin.
func TestChatRowMenuPins(t *testing.T) {
	h := newListHarness(t, testChats(1))
	first := h.rightClick(3)
	if got := h.list.menu.shown; len(got) != 1 || got[0] != chatRowPin {
		t.Fatalf("menu of an unpinned chat: %v", got)
	}
	h.press(pointer.ButtonPrimary, first)
	h.frames(5)
	if got := h.store.asked(); len(got) != 1 || got[0] != 4 {
		t.Fatalf("asked to pin %v, want chat 4", got)
	}
	h.frames(30)
	if h.list.menu.open {
		t.Fatal("the menu stays open after its item")
	}

	first = h.rightClick(0)
	if got := h.list.menu.shown; len(got) != 1 || got[0] != chatRowUnpin {
		t.Fatalf("menu of a pinned chat: %v", got)
	}
	h.press(pointer.ButtonPrimary, first)
	h.frames(5)
	if got := h.store.asked(); len(got) != 2 || got[1] != -1 {
		t.Fatalf("asked %v, want chat 1 unpinned", got)
	}
}

// TestChatRowMenuLimit checks that past the limit nothing is asked of the
// store, and the toast tells the limit and what Premium makes it.
func TestChatRowMenuLimit(t *testing.T) {
	h := newListHarness(t, testChats(5))
	first := h.rightClick(7)
	h.press(pointer.ButtonPrimary, first)
	h.frames(5)
	if got := h.store.asked(); len(got) != 0 {
		t.Fatalf("asked %v with five chats pinned", got)
	}
	if want := "You can pin no more than 5 chats. With Telegram Premium, up to 10."; h.list.toast.Text() != want {
		t.Fatalf("toast %q, want %q", h.list.toast.Text(), want)
	}

	// With Premium the limit is ten.
	h = newListHarness(t, testChats(5))
	h.store.premium = model.PremiumFromConfig(true, nil)
	first = h.rightClick(7)
	h.press(pointer.ButtonPrimary, first)
	h.frames(5)
	if got := h.store.asked(); len(got) != 1 {
		t.Fatalf("asked %v with Premium and five pinned", got)
	}
}

// TestChatRowMenuRefused checks that a limit the server names, or any
// failure, comes to the toast.
func TestChatRowMenuRefused(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{model.ErrPinnedTooMuch, "You can pin no more than 5 chats. With Telegram Premium, up to 10."},
		{errors.New("boom"), "Could not change the pinned chats"},
	} {
		h := newListHarness(t, testChats(1))
		h.store.fail = tc.err
		first := h.rightClick(3)
		h.press(pointer.ButtonPrimary, first)
		deadline := time.Now().Add(2 * time.Second)
		for h.list.toast.Text() == "" && time.Now().Before(deadline) {
			h.frames(1)
			time.Sleep(time.Millisecond)
		}
		if h.list.toast.Text() != tc.want {
			t.Fatalf("%v: toast %q, want %q", tc.err, h.list.toast.Text(), tc.want)
		}
	}
}

// TestChatRowMenuRead checks that a chat with unread messages offers to mark
// it read, and one without does not.
func TestChatRowMenuRead(t *testing.T) {
	chats := testChats(0)
	chats[2].Unread = 3
	h := newListHarness(t, chats)
	first := h.rightClick(2)
	if got := h.list.menu.shown; len(got) != 2 || got[1] != chatRowRead {
		t.Fatalf("menu of a chat with unread messages: %v", got)
	}
	// The second item.
	h.press(pointer.ButtonPrimary, f32.Pt(first.X, first.Y+float32(menuItemHeight)))
	h.frames(5)
	h.store.mu.Lock()
	read := h.store.read
	h.store.mu.Unlock()
	if len(read) != 1 || read[0] != 3 {
		t.Fatalf("marked read %v, want chat 3", read)
	}
	h.frames(30)
	h.rightClick(4)
	if got := h.list.menu.shown; len(got) != 1 {
		t.Fatalf("menu of a read chat: %v", got)
	}
}

// TestChatRowMenuOnlyInAllChats checks that a folder's list has no pins: they
// are the list of all chats'.
func TestChatRowMenuOnlyInAllChats(t *testing.T) {
	h := newListHarness(t, testChats(0))
	h.sec = section{kind: sectionFolder, folder: 1}
	h.frames(2)
	h.press(pointer.ButtonSecondary, rowAt(1))
	h.frames(30)
	if h.list.menu.open {
		t.Fatal("a menu opened with nothing to offer")
	}
}
