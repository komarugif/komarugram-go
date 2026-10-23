// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/layout"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/motion"
)

func TestSettingsLayoutPanicRecovery(t *testing.T) {
	w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	defer w.Motion.Close()
	a := New(w, mockstore.New(time.Now(), 0), Services{})
	defer a.Close()
	a.section = section{kind: sectionSettings}
	h := &focusHarness{draw: func(gtx layout.Context) { a.Update(gtx); a.Layout(gtx) }}
	h.frame()
	if a.recoverSettings() {
		t.Fatal("successful layout must not trigger recovery")
	}
	// Recreate the radio contract violation inside the real settings list.
	// Keep it broken: recovery must leave the section, not retry its layout.
	c := a.settings.decoders.stickers
	c.options = append(c.options, "invalid")
	for range 2 {
		a.settings.section = settingsIntegrations
		var failure any
		func() {
			defer func() { failure = recover() }()
			h.frame()
		}()
		if failure == nil {
			t.Fatal("fault injection did not panic")
		}
		if !a.RecoverFrame() || a.settings.section != settingsMain {
			t.Fatal("failed subsection did not return to settings")
		}
		// Gio's input router consumes the recovered frames too. Reusing the
		// unfinished list or any of the failed frame's macros panics here.
		h.frame()
		h.frame()
		if a.settings.toast.Text() == "" {
			t.Fatal("recovery did not explain the navigation")
		}
		// Navigate through actual click handling after recovery.
		a.settings.items[settingsAppearance].click.Click()
		h.frame()
		if a.settings.section != settingsAppearance {
			t.Fatal("settings stopped handling input after recovery")
		}
		a.settings.open()
		h.frame()
	}
	// A failure in the main page is not the subsection's: the window
	// tries once more, then leaves for the chats.
	a.settings.layingOut = true
	if !a.RecoverFrame() || a.section.kind != sectionSettings {
		t.Fatal("the first failure of the main page was not tried again")
	}
	if !a.RecoverFrame() || a.section.kind != sectionAll {
		t.Fatal("a failing main page did not leave for the chats")
	}
}

// A chat that panics in every frame is tried once more, then closed, for
// the window to show its list of chats instead of its panic screen; and
// once nothing is left to close, the panic screen stays, not redrawn in a
// loop.
func TestRepeatedPanicLeavesToChats(t *testing.T) {
	w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	defer w.Motion.Close()
	a := New(w, mockstore.New(time.Now(), 0), Services{})
	defer a.Close()
	h := &focusHarness{draw: func(gtx layout.Context) { a.Update(gtx); a.Layout(gtx) }}
	h.frame()
	chat := a.store.Chats()[0].ID
	a.open(chatPick{ID: chat})
	h.frame()
	if a.history.chat != chat {
		t.Fatalf("chat %d did not open", chat)
	}
	panicked := func() bool {
		failed := false
		func() {
			defer func() { failed = recover() != nil }()
			h.frame()
		}()
		return failed
	}
	// The days of the messages are kept until the history changes: without
	// them, every row panics.
	a.history.dayStart = nil
	if !panicked() {
		t.Fatal("fault injection did not panic")
	}
	if !a.RecoverFrame() || a.selected != chat {
		t.Fatal("the first panic was not tried again in the chat")
	}
	if !panicked() {
		t.Fatal("the chat stopped panicking by itself")
	}
	if !a.RecoverFrame() || a.selected != 0 || a.chats.toast.Text() == "" {
		t.Fatal("a chat panicking again was not closed, with a word why")
	}
	if panicked() {
		t.Fatal("the list of chats panicked")
	}
	// The chat opens anew, and is drawn.
	a.open(chatPick{ID: chat})
	if panicked() || a.history.chat != chat {
		t.Fatal("the chat did not open again")
	}
	// What panics with nothing left to close keeps the panic screen.
	a.panics = 2
	if a.RecoverFrame() {
		t.Fatal("recovery with nothing left to close asked for another frame")
	}
}
