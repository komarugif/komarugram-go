// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// In a group the sender's avatar is beside the message on its side: at the
// start of one received, at the end of one sent, where its bubble is.
func TestAvatarOnMessageSide(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[1].Outgoing, messages[1].SenderID = true, 99
		messages[0].SenderID = 42
	})
	h.page.avatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Dp(size), gtx.Dp(size))}
	}
	h.frames(2)
	in, out := h.page.rows[1], h.page.rows[2]
	if in == nil || out == nil {
		t.Fatal("rows not laid out")
	}
	if in.avatarPoint.X != in.bodyLeft {
		t.Errorf("a received message's avatar at %d, its message at %d", in.avatarPoint.X, in.bodyLeft)
	}
	if want := out.bodyLeft + out.bodySize.X - 34; out.avatarPoint.X != want || out.bodyLeft <= in.bodyLeft {
		t.Errorf("a sent message's avatar at %d, want %d at the end of its message (%d+%d)", out.avatarPoint.X, want, out.bodyLeft, out.bodySize.X)
	}
}

// A sent message as wide as the history leaves its avatar its place: the
// bubble took the whole width, the avatar laid out after it, and the
// avatar was drawn over the bubble's end, the more the longer the text.
func TestAvatarBesideLongSentMessage(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[0].Outgoing, messages[0].SenderID = true, 99
		messages[0].Text = strings.Repeat("A long message that fills the bubble. ", 6)
	})
	red := color.NRGBA{R: 255, A: 255}
	h.page.avatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
		s := image.Pt(gtx.Dp(size), gtx.Dp(size))
		paint.FillShape(gtx.Ops, red, clip.Rect{Max: s}.Op())
		return layout.Dimensions{Size: s}
	}
	h.frames(2)
	size := image.Pt(500, 700)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skip("no GPU:", err)
	}
	defer win.Release()
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Now: h.now, Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	bubble := scheme(gtx).PrimaryContainer.Color.AsNRGBA()
	h.page.Layout(gtx, model.Chat{ID: 1, Title: "Chat", Kind: model.KindGroup}, localization.For("en"), false)
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	near := func(c color.RGBA, want color.NRGBA) bool {
		d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
		return d(c.R, want.R) < 6 && d(c.G, want.G) < 6 && d(c.B, want.B) < 6
	}
	found := false
	for y := 0; y < size.Y; y++ {
		for x := size.X / 2; x < size.X; x++ {
			if !near(img.RGBAAt(x, y), red) {
				continue
			}
			found = true
			// The avatar's left edge: the gap before it is no bubble.
			if gap := img.RGBAAt(x-4, y); near(gap, bubble) {
				t.Fatalf("the bubble runs under the avatar at (%d, %d)", x, y)
			}
			break
		}
	}
	if !found {
		t.Fatal("no avatar beside the sent message")
	}
}
