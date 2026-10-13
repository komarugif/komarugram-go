// SPDX-License-Identifier: Unlicense OR MIT

package wdk

import (
	"image"
	"testing"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"golang.org/x/exp/shiny/materialdesign/icons"
)

// TestIconWidgetKeepsEachColor draws an icon in two colors by turns, as the
// kind of a chat is in the selected row of a list and in the others: each
// color is rasterized once, not again on every turn.
func TestIconWidgetKeepsEachColor(t *testing.T) {
	icon := RequireIconWidget(icons.SocialPeople)
	ops := new(op.Ops)
	draw := func(c token.MatColor) {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(16, 16))}
		icon(gtx, c)
	}
	a, b := token.NewMatColorFromHexRGB(0x202020), token.NewMatColorFromHexRGB(0xFFFFFF)
	draw(a)
	draw(b)
	if allocs := testing.AllocsPerRun(20, func() { draw(a); draw(b) }); allocs > 0 {
		t.Fatalf("drawing an icon in two colors allocates %.0f times a turn", allocs)
	}
}
