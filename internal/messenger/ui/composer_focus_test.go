// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"

	"komarugram/internal/messenger/shortcuts"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
)

// typed types text into whatever holds the keyboard and returns what the
// message field of chat has then.
func (h *composerHarness) typed(chat int64, text string) string {
	h.router.Queue(key.EditEvent{Text: text})
	h.frame()
	return h.p.composer.draft(chat).editor.Text()
}

// The message field holds the keyboard of a chat just opened, without a
// click, and of the next chat opened, as Telegram Desktop's does.
func TestComposerHoldsTheKeyboardOfAnOpenedChat(t *testing.T) {
	h := newComposerHarness(t)
	h.frame()
	if got := h.typed(1, "a"); got != "a" {
		t.Fatalf("typed in a chat just opened, the field has %q", got)
	}
	h.chat = 2
	h.frame()
	h.frame()
	if got := h.typed(2, "b"); got != "b" {
		t.Fatalf("typed in the next chat opened, the field has %q", got)
	}
	if got := h.p.composer.draft(1).editor.Text(); got != "a" {
		t.Fatalf("the first chat's field has %q", got)
	}
}

// What else takes the keyboard keeps it while it is there, and the field
// takes it back once it is gone.
func TestComposerGivesTheKeyboardAndTakesItBack(t *testing.T) {
	h := newComposerHarness(t)
	h.frame()
	var other struct{}
	shown, typed := true, ""
	h.over = func(gtx layout.Context) {
		if !shown {
			return
		}
		for {
			ev, ok := gtx.Event(key.FocusFilter{Target: &other})
			if !ok {
				break
			}
			if e, ok := ev.(key.EditEvent); ok {
				typed += e.Text
			}
		}
		event.Op(gtx.Ops, &other)
	}
	h.frame()
	h.router.Source().Execute(key.FocusCmd{Tag: &other})
	h.frame()
	h.frame()
	if got := h.typed(1, "a"); got != "" || typed != "a" {
		t.Fatalf("with another field focused, the message field has %q and the other %q", got, typed)
	}
	shown = false
	h.frame()
	h.frame()
	if got := h.typed(1, "b"); got != "b" {
		t.Fatalf("the other field gone, the message field has %q", got)
	}
}

// Text selected in the history keeps the keyboard, for copying it; Escape
// drops the selection and the field takes the keyboard back.
func TestComposerLeavesTheKeyboardToASelection(t *testing.T) {
	h := newComposerHarness(t)
	h.frame()
	var row *messageRow
	for _, r := range h.p.rows {
		if len(r.text.fragments) > 0 {
			row = r
			break
		}
	}
	if row == nil {
		t.Fatal("no message with text in the history")
	}
	row.text.anchor, row.text.caret = 0, 1
	h.p.activeText = row
	h.router.Source().Execute(key.FocusCmd{Tag: &h.p.keyboard})
	h.frame()
	h.frame()
	if !h.router.Source().Focused(&h.p.keyboard) {
		t.Fatal("the history let go of the keyboard with text selected")
	}
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame()
	h.frame()
	if got := h.typed(1, "a"); got != "a" {
		t.Fatalf("after Escape dropped the selection, the field has %q", got)
	}
}

// The chat's search opens by its shortcut from the message field holding
// the keyboard, as Telegram Desktop's does while typing.
func TestChatSearchOpensFromTheMessageField(t *testing.T) {
	h := newComposerHarness(t)
	h.p.shortcuts = func() shortcuts.Bindings {
		return shortcuts.Bindings{"chat.search": {{Key: "F", Modifiers: key.ModShortcut}}}
	}
	h.frame()
	if !h.p.canSearchChat() {
		t.Skip("the demo store cannot search a chat")
	}
	if !h.router.Source().Focused(&h.p.composer.draft(1).editor) {
		t.Fatal("the message field does not hold the keyboard")
	}
	h.router.Queue(key.Event{Name: "F", Physical: "F", Modifiers: key.ModShortcut, State: key.Press})
	h.frame()
	if !h.p.chatSearch.open {
		t.Fatal("Ctrl+F in the message field did not open the chat's search")
	}
}
