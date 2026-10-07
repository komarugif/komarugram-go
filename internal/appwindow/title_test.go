// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"testing"

	"gioui.org/app"
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
