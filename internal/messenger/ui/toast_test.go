// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"komarugram/internal/messenger/model"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// A toast shows from its first frame until toastDuration later, and a new
// one replaces it and starts its time again.
func TestToastGoesByItself(t *testing.T) {
	var tt toast
	now := time.Now()
	frame := func(at time.Duration) {
		gtx := layout.Context{Ops: new(op.Ops), Now: now.Add(at), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
		tt.Layout(gtx, image.Rect(0, 0, 400, 300))
	}
	tt.Show("Скопировано")
	frame(0)
	frame(toastDuration - time.Millisecond)
	if tt.Text() != "Скопировано" {
		t.Fatal("the toast went before its time")
	}
	tt.Show("Не удалось")
	frame(toastDuration + time.Second)
	frame(toastDuration + time.Second + toastDuration/2)
	if tt.Text() != "Не удалось" {
		t.Fatal("a new toast did not start its time again")
	}
	frame(2*toastDuration + time.Second)
	if tt.Text() != "" {
		t.Fatal("the toast stayed")
	}
}

// A message that could not be sent is told in the history's toast, not
// kept on screen: the toast goes, and Send still retries it.
func TestComposerFailureIsToast(t *testing.T) {
	// From the root, where the demo's media are, so that none fails and
	// takes the toast.
	t.Chdir("../../..")
	h := newComposerHarness(t)
	c := h.p.composer
	f := failingComposer{make(chan model.OutgoingMessage, 2)}
	c.source = f
	d := c.draft(1)
	d.editor.SetText("keep me")
	c.submitText()
	<-f.requests
	for deadline := time.Now().Add(time.Second); d.sending && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
		h.frame()
	}
	h.frame()
	if h.p.toast.Text() != "offline" {
		t.Fatalf("toast %q", h.p.toast.Text())
	}
	for range 5 * 60 {
		h.frame()
	}
	if h.p.toast.Text() != "" {
		t.Fatal("the failure stayed on screen")
	}
	if d.err == nil {
		t.Fatal("the draft forgot it was not sent")
	}
}
