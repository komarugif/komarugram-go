// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"testing"

	"komarugram/internal/messenger/model"
)

// pagingStore is a menuStore whose history asks for its pages of nothing:
// the test brings them.
type pagingStore struct {
	*menuStore
	newer, older int
}

func (s *pagingStore) LoadNewer(int64) { s.newer++ }
func (s *pagingStore) LoadOlder(int64) { s.older++ }

// A list at the end of what is loaded, with newer messages after it, stays
// where it is when the page of them comes, and asks for the next one only
// when scrolled to it: following the end, it went to the end of each page
// as it came, asked for the next, and scrolled on by itself to the chat's
// end, as a scrollbar's thumb let go at the bottom did. At the chat's end,
// a message that comes is still followed.
func TestNewerPageDoesNotPullTheListToItsEnd(t *testing.T) {
	var store *pagingStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &pagingStore{menuStore: s}
		return store
	})
	history := &h.store.benchmarkHistory.h
	history.HasNewer = true
	history.Revision++
	h.page.list.Position.BeforeEnd = false // dragged to the bottom
	h.frames(3)

	add := func(from, to int) {
		for i := from; i <= to; i++ {
			history.Messages = append(history.Messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}, Date: h.now, Text: fmt.Sprintf("Message %d", i), SenderName: "Ann", ContentRevision: 1})
		}
		history.Revision++
	}
	add(21, 40) // the page of newer messages
	asked := store.newer
	h.frames(3)
	pos := h.page.list.Position
	if !pos.BeforeEnd || h.page.messages[pos.First].Key.MessageID > 20 {
		t.Fatalf("the newer page took the list to its end: BeforeEnd %v, from message %d", pos.BeforeEnd, h.page.messages[pos.First].Key.MessageID)
	}
	if store.newer != asked {
		t.Fatalf("the next page was asked for with the list away from its end: %d times", store.newer-asked)
	}

	// At the chat's end, a message that comes is followed.
	history.HasNewer = false
	history.Revision++
	h.page.list.Position.BeforeEnd = false
	h.frames(3)
	add(41, 41)
	h.frames(3)
	if h.page.list.Position.BeforeEnd {
		t.Fatal("at the chat's end, the list did not follow a message that came")
	}
}
