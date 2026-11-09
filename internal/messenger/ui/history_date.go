// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// The logical first row supplies the date; prefix sums locate the next date
// without laying out the intervening history. The next pill pushes this one out.
func (p *chatPage) stickyDate(gtx layout.Context) {
	above := len(p.above)
	first := p.list.Position.First - above
	if first < 0 || first >= len(p.messages) || p.heights == nil {
		return
	}
	day := p.dates[first]
	starts := p.dayStart[first]
	// While the day's own pill, 14 dp into its first row and about 30 dp
	// tall, is in view, it is the date: the sticky one would cover it
	// but for the part above, which showed as a second, clipped date.
	if starts && p.list.Position.Offset <= gtx.Dp(44) {
		return
	}
	nextTop := gtx.Constraints.Max.Y + gtx.Dp(50)
	if i := p.nextDay[first]; i < len(p.messages) {
		nextTop = int(p.heights.Prefix(above+i)-p.heights.Prefix(above+first)) - p.list.Position.Offset + gtx.Dp(14)
	}
	rec := op.Record(gtx.Ops)
	pillGtx := gtx
	pillGtx.Constraints.Min = image.Point{}
	dims := datePill(pillGtx, day)
	call := rec.Stop()
	y := min(gtx.Dp(4), nextTop-dims.Size.Y-gtx.Dp(4))
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	defer op.Offset(image.Pt((gtx.Constraints.Max.X-dims.Size.X)/2, y)).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Local().Date()
	by, bm, bd := b.Local().Date()
	return ay == by && am == bm && ad == bd
}
