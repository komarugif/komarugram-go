// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"sync"
	"testing"
	"time"

	"gioui.org/io/input"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// revealingStore empties a history a jump reveals until load, as tgstore
// does while it loads the messages around the one jumped to.
type revealingStore struct {
	*mockstore.Store
	mu        sync.Mutex
	revealing bool
}

func (s *revealingStore) Reveal(chat int64, id model.MessageID) {
	s.mu.Lock()
	s.revealing = true
	s.mu.Unlock()
	s.Store.Reveal(chat, id)
}

func (s *revealingStore) load() {
	s.mu.Lock()
	s.revealing = false
	s.mu.Unlock()
}

func (s *revealingStore) History(chat int64) model.History {
	s.mu.Lock()
	revealing := s.revealing
	s.mu.Unlock()
	if revealing {
		return model.History{LoadingOlder: true}
	}
	return s.Store.History(chat)
}

func (s *revealingStore) HistorySince(chat int64, _ uint64) (model.History, bool) {
	return s.History(chat), true
}

// rings are where the frame drew the rings of loads.
func rings(h *composerHarness) []image.Rectangle {
	want := localization.For("ru").T("history.loading")
	var out []image.Rectangle
	var walk func([]input.SemanticNode)
	walk = func(nodes []input.SemanticNode) {
		for _, n := range nodes {
			if n.Desc.Label == want && !slices.Contains(out, n.Desc.Bounds) {
				out = append(out, n.Desc.Bounds)
			}
			walk(n.Children)
		}
	}
	walk(h.router.AppendSemantics(nil))
	return out
}

// A jump to a message far in the history, which the store loads anew, shows
// its load by the ring over the buttons at the bottom right, never by one
// in the middle of the page; a chat opened anew shows that one.
func TestJumpShowsTheRingOverTheButtons(t *testing.T) {
	store := &revealingStore{Store: mockstore.New(time.Now(), 0)}
	p := newChatPage(store, func() {})
	p.images = &imageOps{}
	t.Cleanup(p.Close)
	h := &composerHarness{p: p, now: time.Now(), size: image.Pt(680, 720), chat: 2, kind: model.KindUser}
	for range 4 {
		h.frame()
	}
	if len(p.messages) == 0 {
		t.Fatal("no history")
	}
	p.jumpTo(100000)
	for range 3 {
		h.frame()
	}
	got := rings(h)
	if len(got) != 1 || got[0].Min.X < h.size.X/2 || got[0].Min.Y < h.size.Y/2 {
		t.Fatalf("rings while the jump loads: %v, want one at the bottom right of %v", got, h.size)
	}
	store.load()
	for range 3 {
		h.frame()
	}
	if len(p.messages) == 0 || p.jumping {
		t.Fatal("the history did not come")
	}

	// Opened anew, a chat without its history shows the ring in the middle.
	store.mu.Lock()
	store.revealing = true
	store.mu.Unlock()
	h.chat = 3
	for range 3 {
		h.frame()
	}
	got = rings(h)
	if len(got) != 1 || got[0].Max.X > h.size.X*3/4 {
		t.Fatalf("rings of a chat opened: %v", got)
	}
}
