// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// An album at the bottom is read up to its last message, not its first,
// which it is drawn as.
func TestBottomMessageAlbum(t *testing.T) {
	msg := func(id model.MessageID) model.Message {
		return model.Message{Key: model.MessageKey{ChatID: 1, MessageID: id}, GroupedID: 7, Media: &model.MessageMedia{}}
	}
	p := chatPage{restored: true, messages: model.GroupAlbums([]model.Message{msg(10), msg(11), msg(12)})}
	p.list.Position.Count = 1
	if id := p.bottomMessage(); id != 12 {
		t.Fatalf("bottom %d, want 12", id)
	}
}

func TestPinnedShown(t *testing.T) {
	ids := []model.MessageID{3, 8, 15}
	for _, c := range []struct {
		bottom, clicked model.MessageID
		want            model.MessageID
		index           int
	}{
		{0, 0, 15, 2},
		{20, 0, 15, 2},
		{15, 0, 15, 2},
		{14, 0, 8, 1},
		{2, 0, 3, 0},
		{20, 15, 8, 1},
		{20, 8, 3, 0},
		// After the oldest, the latest again.
		{20, 3, 15, 2},
	} {
		got, index := pinnedShown(ids, c.bottom, c.clicked)
		if got != c.want || index != c.index {
			t.Errorf("bottom %d, clicked %d: shows %d (#%d), want %d (#%d)", c.bottom, c.clicked, got, index, c.want, c.index)
		}
	}
}

// pinnedStore has pinned messages.
type pinnedStore struct {
	*menuStore
	ids    []model.MessageID
	hidden bool
}

func (s *pinnedStore) PinnedMessages(int64) []model.MessageID {
	if s.hidden {
		return nil
	}
	return s.ids
}
func (s *pinnedStore) HidePinned(int64) { s.hidden = true }

// The bar shows the latest pinned message above the history's bottom; a
// click goes to it and shows the one before, and after the oldest the
// latest again. Its button hides it.
func TestPinnedBarClicks(t *testing.T) {
	var store *pinnedStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &pinnedStore{menuStore: s, ids: []model.MessageID{3, 8, 15}}
		return store
	})
	p := h.page
	p.list.Position.BeforeEnd = false
	h.frames(3)
	shown := func() model.MessageID {
		id, _ := pinnedShown(store.ids, p.pinned.bottom, p.pinned.clicked)
		return id
	}
	if got := shown(); got != 15 {
		t.Fatalf("the bar shows %d, not the latest pinned", got)
	}
	for _, want := range []struct{ at, next model.MessageID }{{15, 8}, {8, 3}, {3, 15}} {
		h.press(pointer.ButtonPrimary, f32.Pt(100, 26))
		h.frames(3)
		first := p.messages[p.list.Position.First].Key.MessageID
		last := p.messages[p.list.Position.First+p.list.Position.Count-1].Key.MessageID
		if want.at < first || want.at > last || shown() != want.next {
			t.Fatalf("a click showed messages %d to %d and then pinned %d, want %d in view and %d", first, last, shown(), want.at, want.next)
		}
	}
	h.press(pointer.ButtonPrimary, f32.Pt(500-4-24, 26))
	h.frames(2)
	if !store.hidden || p.pinnedHeight(layout.Context{}, 1) != 0 {
		t.Fatal("the bar was not hidden")
	}
}

// TestRenderPinned saves the pinned bar over a history, for looking at it:
// PINNED_PNG=/tmp/pinned.png.
func TestRenderPinned(t *testing.T) {
	path := os.Getenv("PINNED_PNG")
	if path == "" {
		t.Skip("set PINNED_PNG to a file")
	}
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		messages[14].Text = "Встреча в пятницу в 18:00, адрес в описании группы"
	}, func(s *menuStore) model.ConversationStore {
		return &pinnedStore{menuStore: s, ids: []model.MessageID{3, 8, 15}}
	})
	p := h.page
	p.list.Position.BeforeEnd = false
	l := localization.For("ru")
	renderFrames(t, image.Pt(500, 600), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}
