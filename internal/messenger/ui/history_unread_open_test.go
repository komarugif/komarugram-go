// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// anchorSource is a history opened at a message still on its way, which
// keeps the views saved.
type anchorSource struct {
	benchmarkHistory
	view  model.Viewport
	saved *[]model.Viewport
}

func (s anchorSource) Viewport(int64) (model.Viewport, bool) { return s.view, true }
func (s anchorSource) SaveView(v model.Viewport, _ []model.MessageLayout) {
	*s.saved = append(*s.saved, v)
}

// A history waiting for the message it opens at, as a chat opened at its
// first unread message, saves no view until it is there: saved, the view of
// the messages the cache had, at their end, took the place of the anchor,
// and the chat opened at its end, read whole.
func TestWaitingHistorySavesNoView(t *testing.T) {
	var messages []model.Message
	for i := 1; i <= 40; i++ {
		messages = append(messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}, Date: time.Unix(1000, 0), Text: fmt.Sprint("Message ", i), ContentRevision: 1})
	}
	var saved []model.Viewport
	source := anchorSource{
		benchmarkHistory: benchmarkHistory{h: model.History{Messages: messages, Revision: 1, LoadingOlder: true, HasOlder: true, HasNewer: true}},
		view:             model.Viewport{ChatID: 1, AnchorMessageID: 81},
		saved:            &saved,
	}
	h := &chatInputHarness{now: time.Unix(1000, 0)}
	h.page = newChatPage(source, func() {})
	t.Cleanup(h.page.Close)
	for end := time.Now().Add(700 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		h.now = h.now.Add(20 * time.Millisecond)
		h.frame()
	}
	if h.page.restored || len(saved) > 0 {
		t.Fatalf("restored %t before the anchor came, and saved %+v", h.page.restored, saved)
	}
}
