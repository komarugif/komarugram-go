// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"slices"
	"testing"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// ghostStore records what the page asks to tell.
type ghostStore struct {
	*menuStore
	ghost model.Ghost
	read  []model.MessageID
	asked []bool
}

func (s *ghostStore) SetGhost(g model.Ghost)             { s.ghost = g }
func (s *ghostStore) Ghost() model.Ghost                 { return s.ghost }
func (s *ghostStore) SetOnline(bool)                     {}
func (s *ghostStore) SendAction(int64, model.ChatAction) {}
func (s *ghostStore) MarkRead(_ int64, id model.MessageID, asked bool) {
	s.read, s.asked = append(s.read, id), append(s.asked, asked)
}

// The history reads what it shows; without read receipts a message's menu
// reads it when asked.
func TestGhostRead(t *testing.T) {
	var store *ghostStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &ghostStore{menuStore: s}
		return store
	})
	h.page.list.Position.BeforeEnd = false
	h.frames(2)
	if len(store.read) == 0 || store.read[len(store.read)-1] != 20 || store.asked[len(store.asked)-1] {
		t.Fatalf("the history read %v %v, not what it shows", store.read, store.asked)
	}
	h.openMenu(19)
	if !slices.Contains(h.page.messageMenu.shown, actionRead) {
		t.Fatal("the menu does not offer to read the message")
	}
	store.read, store.asked = nil, nil
	h.choose(actionRead)
	if !slices.Contains(store.read, 19) || !slices.Contains(store.asked, true) {
		t.Fatalf("read %v %v", store.read, store.asked)
	}
	store.ghost.SendRead = true
	h.page.closeMenu()
	h.frames(30)
	h.openMenu(19)
	if slices.Contains(h.page.messageMenu.shown, actionRead) {
		t.Fatal("with read receipts the menu still offers to read")
	}
}

// The boxes say what Ghost Mode hides: what they leave it keeps telling.
func TestGhostBoxes(t *testing.T) {
	for _, g := range []preferences.Ghost{{On: true}, {On: true, KeepRead: true, ReadOnInteract: true}, {KeepOnline: true, KeepTyping: true, KeepStickers: true, KeepStories: true}} {
		if got := ghostWithHidden(g, ghostHiddenOf(g)); got != g {
			t.Errorf("%+v came back as %+v", g, got)
		}
	}
	if got := ghostHiddenOf(preferences.Ghost{KeepUploads: true}); slices.Contains(got, "uploads") || !slices.Contains(got, "read") || !slices.Contains(got, "stories") {
		t.Errorf("hidden %v", got)
	}
	if got := ghostWithHidden(preferences.Ghost{On: true}, []string{"stories"}); got.KeepStories || !got.KeepRead {
		t.Errorf("stories hidden alone: %+v", got)
	}
}
