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

// What the chat list tells shows in the chat open, in its comments when
// they are, and over the window when no chat is open: never at the bottom
// of the list, too narrow for a long error.
func TestListToastShowsInTheChat(t *testing.T) {
	w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	defer w.Motion.Close()
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{})
	defer a.Close()
	h := &focusHarness{draw: func(gtx layout.Context) { a.Update(gtx); a.Layout(gtx) }}
	h.frame()
	tell := func(text string) {
		t.Helper()
		a.chats.toast.Show(text)
		h.frame()
		if got := a.chats.toast.Text(); got != "" {
			t.Fatalf("the list keeps %q", got)
		}
	}

	tell("no chat")
	if got := a.toast.Text(); got != "no chat" {
		t.Fatalf("over the window %q", got)
	}
	// It blurs the window under it as toasts are to, and is opaque when
	// they are not.
	if !a.overlayPrefs().toasts || a.toast.bd == nil {
		t.Fatalf("toasts blur %v, and the window's blurs %v", a.overlayPrefs().toasts, a.toast.bd != nil)
	}
	o := a.preferences.Global().Overlays
	o.ToastsBlur = false
	if err := a.preferences.SetOverlays(o); err != nil {
		t.Fatal(err)
	}
	h.frame()
	if a.toast.bd != nil {
		t.Fatal("the window's toast blurs with toasts that do not")
	}
	o.ToastsBlur = true
	if err := a.preferences.SetOverlays(o); err != nil {
		t.Fatal(err)
	}
	a.toast.Hide()

	const channel = 4
	a.open(chatPick{ID: channel})
	h.frame()
	tell("in the chat")
	if got := a.history.toast.Text(); got != "in the chat" || a.toast.Text() != "" {
		t.Fatalf("the chat's toast %q, the window's %q", got, a.toast.Text())
	}

	for _, m := range store.History(channel).Messages {
		if m.CommentsOpen {
			a.openComments(m)
			break
		}
	}
	h.frame()
	tell("in the comments")
	if got := a.comments.toast.Text(); got != "in the comments" || a.toast.Text() != "" {
		t.Fatalf("the comments' toast %q, the window's %q", got, a.toast.Text())
	}

	// Settings have no chat to tell it in.
	a.closeComments()
	a.section = section{kind: sectionSettings}
	h.frame()
	tell("in the settings")
	if got := a.toast.Text(); got != "in the settings" {
		t.Fatalf("over the window %q", got)
	}
}
