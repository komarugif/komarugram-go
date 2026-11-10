// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// unverifiedChat is a private chat whose newest 400 messages the cache has
// whole but cannot vouch for: its difference was too long since they were
// read. Its history is open at its end.
func unverifiedChat(t *testing.T) *verifiedChat {
	t.Helper()
	s := testStore(t)
	v := newVerifiedChat(t, s, &tg.PeerUser{UserID: 5}, peerRecord{ID: 5, Hash: 1, Kind: "user"})
	// Edits read with the page, one shown and one hidden (edit_hide, as
	// bots stamp on posts whose buttons change): both dates are in the hash.
	v.edited[760] = 3000
	v.edited[770] = 4000
	v.hidden[770] = true
	v.read()
	s.endLive(0)
	v.open()
	v.settle()
	return v
}

// A page the cache has whole but cannot vouch for is shown at once, not
// asked of Telegram first, and checked when the scroll rests: unchanged,
// Telegram answers with messagesNotModified, and the page is verified.
// Before, the scroll waited for Telegram to send the page again.
func TestUnverifiedPageIsShownThenVerified(t *testing.T) {
	v := unverifiedChat(t)
	v.expectChecked("an unverified page")
	if n := v.same.Load(); n != 1 {
		t.Fatalf("the check of a page unchanged: %d answers not modified, want 1: the hash is not Telegram's", n)
	}
	v.open()
	v.expectVerified("the page checked")
}

// A range that changed meanwhile comes whole from its check, and replaces
// the cache's and the history's in place: the message deleted goes away,
// the one edited shows its edit.
func TestChangedRangeIsReplacedInPlace(t *testing.T) {
	v := unverifiedChat(t)
	v.mu.Lock()
	v.deleted[790] = true
	v.edited[780] = 5000
	v.mu.Unlock()
	if n := v.older(); n != 0 {
		t.Fatalf("%d pages asked of Telegram", n)
	}
	has := func(id model.MessageID) (model.Message, bool) {
		ms := v.s.History(v.chat).Messages
		i := slices.IndexFunc(ms, func(m model.Message) bool { return m.Key.MessageID == id })
		if i < 0 {
			return model.Message{}, false
		}
		return ms[i], true
	}
	if _, ok := has(790); !ok {
		t.Fatal("the cache's page lacks 790 before its check")
	}
	if n := v.settle(); n != 1 {
		t.Fatalf("%d ranges checked, want 1", n)
	}
	if _, ok := has(790); ok {
		t.Fatal("790, deleted, is still shown after the check")
	}
	if m, _ := has(780); m.Text != "edited" || m.EditedAt.IsZero() {
		t.Fatalf("780 after the check: %q, edited %v", m.Text, m.EditedAt)
	}
	v.s.history.mu.Lock()
	chain, _ := v.s.history.chainOf(v.chat)
	v.s.history.mu.Unlock()
	if ok, err := v.s.Cache().Verified(t.Context(), v.chat, 721, 800, chain); err != nil || !ok {
		t.Fatalf("the range replaced is not verified: %v", err)
	}
}

// No check is asked while the scroll goes on.
func TestChecksWaitForTheScroll(t *testing.T) {
	v := unverifiedChat(t)
	v.setRest(300 * time.Millisecond)
	for range 2 {
		v.older()
	}
	// A timer that fires early, as one set before the scroll went on.
	v.s.runChecks()
	time.Sleep(50 * time.Millisecond)
	k := &v.s.history.checks
	k.mu.Lock()
	started := k.running || len(k.queue) != 2
	k.mu.Unlock()
	if n := v.checked.Load(); n != 0 || started {
		t.Fatalf("while the scroll went on: %d ranges checked, a check started: %v", n, started)
	}
	if n := v.settle(); n != 2 {
		t.Fatalf("%d ranges checked once the scroll rested, want 2", n)
	}
}

// No check is asked while the pacer's window lacks room for the pages of a
// scroll that resumes.
func TestChecksLeaveTheScrollRoom(t *testing.T) {
	v := unverifiedChat(t)
	p := &v.s.history.pace
	p.mu.Lock()
	for range paceBudget - checkReserve {
		p.times = append(p.times, time.Now())
	}
	p.mu.Unlock()
	v.older()
	time.Sleep(10 * v.rest())
	k := &v.s.history.checks
	k.mu.Lock()
	waiting := len(k.queue) == 1 && !k.running
	k.mu.Unlock()
	if n := v.checked.Load(); n != 0 || !waiting {
		t.Fatalf("with the window near its budget: %d ranges checked, the check waits in the queue: %v", n, waiting)
	}
	p.mu.Lock()
	p.times = nil
	p.mu.Unlock()
	if n := v.settle(); n != 1 {
		t.Fatalf("%d ranges checked once the window had room, want 1", n)
	}
}

// A range whose chain ended before its check, or whose chat is no longer
// shown, is not checked.
func TestStaleChecksAreDropped(t *testing.T) {
	v := unverifiedChat(t)
	v.setRest(300 * time.Millisecond)
	v.older()
	v.s.endLive(0)
	if n := v.settle(); n != 0 {
		t.Fatalf("a range whose chain ended: %d checked", n)
	}
	v.older()
	v.s.WatchChat(v, 0)
	if n := v.settle(); n != 0 {
		t.Fatalf("a range of a chat not shown: %d checked", n)
	}
}

// A check refused with FLOOD_WAIT stops the checks for the wait, and is not
// the history's error: the user asked for nothing.
func TestRefusedCheckPausesTheChecks(t *testing.T) {
	v := unverifiedChat(t)
	v.mu.Lock()
	v.refuse = tgerr.New(420, "FLOOD_WAIT_3")
	v.mu.Unlock()
	v.older()
	if n := v.settle(); n != 1 {
		t.Fatalf("%d ranges checked, want 1", n)
	}
	if h := v.s.History(v.chat); h.Err != nil {
		t.Fatalf("a refused check is the history's error: %v", h.Err)
	}
	v.older()
	time.Sleep(20 * v.rest())
	if n := v.checked.Load(); n != 0 {
		t.Fatalf("%d ranges checked during the wait", n)
	}
}
