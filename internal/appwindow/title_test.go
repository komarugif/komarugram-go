// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/unit"
)

// SetTitle, which any goroutine calls, asks nothing of the window itself:
// the window's goroutine gives it the title, once, at its next event.
func TestSetTitleLeavesTheWindowToItsGoroutine(t *testing.T) {
	var asked []string
	old := setOption
	setOption = func(w *Window, opts ...app.Option) {
		for _, o := range opts {
			var cnf app.Config
			o(unit.Metric{}, &cnf)
			asked = append(asked, cnf.Title)
		}
	}
	t.Cleanup(func() { setOption = old })
	w := &Window{Window: new(app.Window)}
	w.SetTitle("KomaruGram — Ada")
	if len(asked) != 0 {
		t.Fatalf("SetTitle asked the window for %q itself", asked)
	}
	w.applyTitle()
	w.applyTitle()
	if len(asked) != 1 || asked[0] != "KomaruGram — Ada" {
		t.Fatalf("the window's goroutine asked for %q, want the title once", asked)
	}
	w.SetTitle("KomaruGram — Ada")
	w.applyTitle()
	if len(asked) != 1 {
		t.Fatalf("the same title was asked for again: %q", asked)
	}
}

// PerformLater returns while the actions wait for the main thread, and
// they are performed once it is free.
func TestPerformLaterDoesNotWait(t *testing.T) {
	free := make(chan struct{})
	done := make(chan system.Action, 1)
	old := perform
	perform = func(w *Window, actions system.Action) {
		<-free
		done <- actions
	}
	t.Cleanup(func() { perform = old })
	w := &Window{Window: new(app.Window)}
	returned := make(chan struct{})
	go func() {
		w.PerformLater(system.ActionRaise)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("PerformLater waited for the main thread")
	}
	close(free)
	select {
	case a := <-done:
		if a != system.ActionRaise {
			t.Fatalf("performed %v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the actions were not performed")
	}
}
