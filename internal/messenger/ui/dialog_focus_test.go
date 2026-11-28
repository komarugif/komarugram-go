// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"os"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"komarugram/internal/messenger/model"
)

func TestForwardRequiresConfirmation(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.still = true
	h.page.chats = func() []model.Chat { return []model.Chat{{ID: 9, Kind: model.KindUser, Title: "Friend"}} }
	h.page.openForwardParts(layout.Context{Ops: new(op.Ops)}, h.page.messages[:1], false)
	f := &h.page.forwarding
	var navigated int64
	f.navigate = func(chat model.Chat) { navigated = chat.ID }
	h.frames(3)
	f.search.SetText("Friend")
	h.frame()
	f.rows[9].click.Click()
	h.frame()
	if !f.confirming || f.busy {
		t.Fatal("choosing a chat did not wait for confirmation")
	}
	f.back.click.Click()
	h.frames(3)
	if f.confirming || !f.modal.Shown() || f.search.Text() != "Friend" {
		t.Fatal("back did not restore the picker")
	}
	h.store.mu.Lock()
	sent := len(h.store.forwarded)
	h.store.mu.Unlock()
	if sent != 0 {
		t.Fatal("forwarded before confirmation")
	}
	f.rows[9].click.Click()
	h.frames(2)
	f.goToMessage = true
	f.confirm.click.Click()
	h.frame()
	waitFor(t, "forward confirmed", h.frame, func() bool {
		h.store.mu.Lock()
		defer h.store.mu.Unlock()
		return len(h.store.forwarded) == 1
	})
	waitFor(t, "open forwarded chat", h.frame, func() bool { return navigated == 9 })
}

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

// Folder chips use the same recipient filtering as the media viewer.
func TestForwardFolderFilter(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.still = true
	demo := mockstore.New(time.Now(), 0)
	p := h.page
	p.chats = demo.Chats
	f := &p.forwarding
	f.folders = demo.Folders
	f.usernames = demo
	p.openForwardParts(layout.Context{Ops: new(op.Ops)}, p.messages[:1], false)
	h.frame()
	f.chips.chip(3).click.Click()
	h.frame()
	name := localization.For("en")
	gtx := layout.Context{Ops: new(op.Ops)}
	hits := p.forwardTargets(gtx, name)
	if len(hits) == 0 {
		t.Fatal("expected channels")
	}
	for _, chat := range hits {
		if chat.Kind != model.KindChannel {
			t.Fatal("non-channel in Channels")
		}
	}
	if dir := os.Getenv("FORWARD_PICKER_PNG_DIR"); dir != "" {
		s := newEffectsShots(t, h, dir)
		s.save("forward-picker", s.take())
	}
	f.search.SetText("@ANNA_S")
	if got := p.forwardTargets(gtx, name); len(got) != 0 {
		t.Fatal("search bypassed selected folder")
	}
	f.chips.chip(0).click.Click()
	if got := p.forwardTargets(gtx, name); len(got) != 1 || got[0].ID != 2 {
		t.Fatal("All chats did not restore matching recipient")
	}
}
