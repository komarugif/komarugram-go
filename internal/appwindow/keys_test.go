// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
)

// Escape reaches the content of a window that does not close on it, whose
// menus and dialogs close on it; a window that closes on it quits.
func TestEscapeIsTheContentsUnlessItQuits(t *testing.T) {
	for _, quit := range []bool{false, true} {
		var r input.Router
		w := &Window{}
		frame := func() (quits, contentGot bool) {
			gtx := layout.Context{Ops: new(op.Ops), Source: r.Source()}
			quits = w.handleKeys(gtx, Options{QuitOnEscape: quit})
			for {
				ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
				if !ok {
					break
				}
				if e, ok := ev.(key.Event); ok && e.State == key.Press {
					contentGot = true
				}
			}
			r.Frame(gtx.Ops)
			return quits, contentGot
		}
		frame()
		r.Queue(key.Event{Name: key.NameEscape, State: key.Press}, key.Event{Name: key.NameEscape, State: key.Release})
		quits, contentGot := frame()
		if quits != quit || contentGot == quit {
			t.Errorf("QuitOnEscape %v: quits %v, the content got Escape %v", quit, quits, contentGot)
		}
	}
}
