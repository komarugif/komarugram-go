// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"
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

// A message's time goes on the text's last line when it fits there, as
// Telegram's clients put it, and on a line of its own when it does not:
// the longest text that keeps it on its line in "15:04" moves it to a line
// of its own in "15:04:05", which is wider.
func TestTimeOnLastLine(t *testing.T) {
	// inline lays out a message of n letters, and a short one, and tells
	// whether the time of the first is on its line, as the short one's
	// is: it is no taller.
	inline := func(n int, seconds bool) bool {
		h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
			messages[0].Text = strings.Repeat("a", n)
			messages[1].Text = "a"
			// Apart, each with its sender's name.
			messages[0].SenderID, messages[1].SenderID, messages[1].SenderName = 1, 2, "Bob"
		})
		look := lookOf(layout.Context{})
		look.Seconds = seconds
		gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Exact(image.Pt(500, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		withLook(gtx, look)
		h.page.Layout(gtx, model.Chat{ID: 1, Title: "Chat", Kind: model.KindGroup}, localization.For("en"), false)
		long, short := h.page.rows[1], h.page.rows[2]
		if long == nil || short == nil {
			t.Fatal("rows not laid out")
		}
		return long.bodySize.Y == short.bodySize.Y
	}
	if !inline(1, false) || !inline(1, true) {
		t.Fatal("the time of a short message is not on its line")
	}
	// The most letters with the time on their line.
	lo, hi := 1, 200
	if inline(hi, false) {
		t.Fatal("the time of a full line is on it")
	}
	for hi-lo > 1 {
		if mid := (lo + hi) / 2; inline(mid, false) {
			lo = mid
		} else {
			hi = mid
		}
	}
	if inline(lo, true) {
		t.Errorf("%d letters keep a time with seconds on their line, as they keep one without", lo)
	}
}
