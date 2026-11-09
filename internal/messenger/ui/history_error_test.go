// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/localization"
)

// A history that fails to load says so in the toast, once for each
// failure, and a FLOOD_WAIT says how long to wait; a history that failed
// with nothing to show keeps its retry. A failure showed only as the retry
// button, and an empty history had none: it could not be asked for again
// until the client started again.
func TestHistoryErrorIsToldAndRetried(t *testing.T) {
	h := newMenuHarnessOn(t, nil, nil)
	l := localization.For("en")
	want := l.Format("history.flood_wait", map[string]string{"time": waitText(12*time.Second, l)})

	h.store.benchmarkHistory.h.Err = tgerr.New(420, "FLOOD_WAIT_12")
	h.store.benchmarkHistory.h.Revision++
	h.frames(2)
	if got := h.page.toast.Text(); got != want {
		t.Fatalf("the toast says %q, want %q", got, want)
	}
	h.page.toast.Show("")
	h.frames(2)
	if got := h.page.toast.Text(); got != "" {
		t.Fatalf("the same failure is told again: %q", got)
	}

	// A jump to an end empties the page for the history started again,
	// which fails with no message: told again, and still retried.
	h.page.revealed(jumpStart)
	h.store.benchmarkHistory.h.Err = tgerr.New(420, "FLOOD_WAIT_12")
	h.store.benchmarkHistory.h.Messages = nil
	h.store.benchmarkHistory.h.Revision++
	h.frames(2)
	if got := h.page.toast.Text(); got != want {
		t.Fatalf("a new failure: the toast says %q", got)
	}
	if !h.page.retryShown {
		t.Fatal("an empty history that failed has no retry")
	}
}
