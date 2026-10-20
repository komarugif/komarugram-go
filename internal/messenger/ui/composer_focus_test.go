// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"

	"komarugram/internal/messenger/shortcuts"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
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

// The picker's search takes the keyboard when the picker opens, without a
// click, and the message field takes it back once the picker closes, by
// Escape or by a click beside it.
func TestPickerSearchHoldsTheKeyboardWhileOpen(t *testing.T) {
	h := newComposerHarness(t)
	h.frame()
	c := h.p.composer
	for _, close := range []string{"escape", "beside"} {
		// The emoji button, before the microphone at the end of an empty
		// field's capsule.
		c.draft(1).editor.SetText("")
		h.frame()
		h.click(580, 680)
		if !c.pickerOpen {
			t.Fatal("the emoji button did not open the picker")
		}
		h.frame()
		h.frame()
		h.router.Queue(key.EditEvent{Text: "кот"})
		h.frame()
		if got := c.search.Text(); got != "кот" {
			t.Fatalf("typed with the picker just opened, its search has %q", got)
		}
		c.search.SetText("")
		before := c.draft(1).editor.Text()
		if close == "escape" {
			h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
			h.frame()
		} else {
			// Left of the picker, which lies along the capsule's end.
			h.click(100, 200)
		}
		h.frame()
		h.frame()
		if c.pickerOpen {
			t.Fatalf("%s left the picker open", close)
		}
		if got := h.typed(1, "a"); got != before+"a" {
			t.Fatalf("the picker closed by %s, the field has %q", close, got)
		}
	}
}

// move moves the mouse to x, y, without a button down.
func (h *composerHarness) move(x, y float32) {
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(x, y)})
	h.frame()
	h.frame()
}

// The pointer over the emoji button opens the picker, as Telegram Desktop's
// tabbed panel, which leaves the keyboard to the message field; the pointer
// may cross to the picker, and the picker closes 300ms after the pointer
// left both. A click pins it, and gives the keyboard to its search.
func TestPickerOpensUnderThePointer(t *testing.T) {
	h := newComposerHarness(t)
	c := h.p.composer
	h.frame()
	h.move(580, 680) // the emoji button of an empty field
	if !c.pickerOpen || !c.pickerHovered {
		t.Fatal("the pointer over the emoji button did not open the picker")
	}
	if got := h.typed(1, "a"); got != "a" {
		t.Fatalf("with the picker the pointer opened, the field has %q", got)
	}
	c.draft(1).editor.SetText("")
	h.move(450, 400) // over the picker
	framesOf(h, 30)
	if !c.pickerOpen {
		t.Fatal("the picker closed with the pointer over it")
	}
	h.move(100, 200) // over the history
	framesOf(h, 12) // 200ms
	if !c.pickerOpen {
		t.Fatal("the picker closed before 300ms")
	}
	framesOf(h, 10)
	if c.pickerOpen {
		t.Fatal("the picker stayed after the pointer left it")
	}

	// A click on the button pins the picker the pointer opened.
	h.move(580, 680)
	if !c.pickerOpen {
		t.Fatal("the pointer back over the button did not open the picker")
	}
	h.click(580, 680)
	h.move(100, 200)
	framesOf(h, 30)
	if !c.pickerOpen || c.pickerHovered {
		t.Fatal("a pinned picker closed when the pointer left it")
	}
	h.router.Queue(key.EditEvent{Text: "кот"})
	h.frame()
	if got := c.search.Text(); got != "кот" {
		t.Fatalf("typed in a pinned picker, its search has %q", got)
	}
	// Another click closes it, and it does not open again under the
	// pointer still over the button.
	c.search.SetText("")
	h.move(580, 680)
	h.click(580, 680)
	framesOf(h, 5)
	if c.pickerOpen {
		t.Fatal("a click on the button did not close the pinned picker, or the pointer opened it again")
	}
}

// With "Open the emoji panel only on click", the pointer over the button
// does not open the picker.
func TestPickerOpensOnlyOnClickWhenSet(t *testing.T) {
	h := newComposerHarness(t)
	c := h.p.composer
	c.input = func() (bool, bool) { return false, true }
	h.frame()
	h.move(580, 680)
	framesOf(h, 5)
	if c.pickerOpen {
		t.Fatal("the pointer opened the picker set to open on click")
	}
	h.click(580, 680)
	if !c.pickerOpen || c.pickerHovered {
		t.Fatal("a click did not open the picker")
	}
}

// With "Focus the message field only on click", the field takes the
// keyboard only when clicked, as it did before it held it.
func TestComposerManualFocusWhenSet(t *testing.T) {
	h := newComposerHarness(t)
	h.p.composer.input = func() (bool, bool) { return true, false }
	framesOf(h, 3)
	if got := h.typed(1, "a"); got != "" {
		t.Fatalf("typed in a chat just opened, the field set to take the keyboard on click has %q", got)
	}
	h.click(160, 680)
	if got := h.typed(1, "b"); got != "b" {
		t.Fatalf("typed after a click, the field has %q", got)
	}
}
