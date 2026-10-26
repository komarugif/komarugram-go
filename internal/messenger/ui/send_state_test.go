// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// The line of a message's time is as wide on its way as once sent or
// read: the clock takes the square of the checks, though drawn smaller.
func TestSendStateFooterWidth(t *testing.T) {
	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Constraints{Max: image.Pt(500, 100)}, Metric: unit.Metric{PxPerDp: 2, PxPerSp: 2}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	m := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 5}, Text: "Hi", Outgoing: true}
	l := localization.For("en")
	widths := map[model.SendState]int{}
	for _, state := range []model.SendState{model.SendSending, model.SendSent, model.SendRead} {
		if got := sendStateMark(gtx, state, sendStateMarkSize, scheme(gtx).Primary.Color).Size; got != image.Pt(gtx.Dp(sendStateMarkSize), gtx.Dp(sendStateMarkSize)) {
			t.Errorf("the mark of %v is %v", state, got)
		}
		widths[state] = messageFooter(gtx, m, l, false, state).Size.X
	}
	if widths[model.SendSending] != widths[model.SendSent] || widths[model.SendSent] != widths[model.SendRead] {
		t.Fatalf("the footers are %v wide", widths)
	}
}

// A message on its way, replaced by the one Telegram made of it, keeps
// its bubble's width and place.
func TestSentMessageKeepsWidth(t *testing.T) {
	for _, text := range []string{"Hi", "A message long enough to take the bubble's whole width, and wrap"} {
		h := newMenuHarnessOn(t, nil, nil)
		msgs := h.store.benchmarkHistory.h.Messages
		pending := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: -1000001}, Date: h.now, Text: text, Outgoing: true, Sending: true, ContentRevision: 1}
		h.store.benchmarkHistory.h.Messages = append(msgs, pending)
		h.store.benchmarkHistory.h.Revision++
		h.frame()
		// At the history's end, where the message is.
		h.page.list.Position.First, h.page.list.Position.BeforeEnd = 15, false
		h.frames(60)
		before := h.page.rows[pending.Key.MessageID]
		if before == nil {
			t.Fatal("the message on its way is not drawn")
		}
		sent := pending
		sent.Key.MessageID, sent.Sending = 21, false
		h.store.benchmarkHistory.h.Messages = append(msgs, sent)
		h.store.benchmarkHistory.h.Revision++
		h.frames(60)
		after := h.page.rows[sent.Key.MessageID]
		if after == nil {
			t.Fatal("the message sent is not drawn")
		}
		if after.bodySize != before.bodySize || after.bodyLeft != before.bodyLeft {
			t.Fatalf("%q: on its way %v at %d, sent %v at %d", text, before.bodySize, before.bodyLeft, after.bodySize, after.bodyLeft)
		}
	}
}
