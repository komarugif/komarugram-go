// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"

	"gioui.org/io/key"
)

// The search of the dialog that forwards takes the keyboard when the
// dialog opens, from the message field under it: what is typed searches
// the chats, and does not go into the message.
func TestForwardSearchTakesTheKeyboard(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.frames(3)
	h.openMenu(5)
	h.choose(actionForward)
	h.frames(20)
	h.router.Queue(key.EditEvent{Text: "Сохр"})
	h.frames(3)
	if got := h.page.forwarding.search.Text(); got != "Сохр" {
		t.Errorf("the search has %q", got)
	}
	if got := h.page.composer.draft(1).editor.Text(); got != "" {
		t.Errorf("the message field took %q", got)
	}
	// Closed, the dialog gives the keyboard back to the field.
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frames(30)
	h.router.Queue(key.EditEvent{Text: "hi"})
	h.frames(3)
	if got := h.page.composer.draft(1).editor.Text(); got != "hi" {
		t.Errorf("after the dialog the field has %q", got)
	}
}

// A dialog without a field holds the keyboard itself, while it is open:
// what is typed does not go into the message field under it.
func TestDialogKeepsTypingFromTheField(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.frames(3)
	h.openMenu(6)
	h.choose(actionDelete)
	h.frames(20)
	if !h.page.deletion.modal.Shown() {
		t.Fatal("the delete dialog did not open")
	}
	h.router.Queue(key.EditEvent{Text: "x"})
	h.frames(3)
	if got := h.page.composer.draft(1).editor.Text(); got != "" {
		t.Errorf("the message field under the dialog took %q", got)
	}
}
