// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// messageResize survives content revisions. Inner disclosures already animate
// their layouts, so the outer transition runs only for a revised message.
type messageResize struct {
	height    heightTransition
	changing  bool
	rowHeight int
}

func (r *messageResize) layout(gtx layout.Context, animate bool, content layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := content(gtx)
	call := macro.Stop()
	target := dims.Size.Y
	dims.Size.Y = r.height.Value(gtx, target, animate && r.changing)
	r.changing = dims.Size.Y != target
	if r.changing {
		// Both drawing and event handlers follow the visible surface.
		defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
	}
	call.Add(gtx.Ops)
	return dims
}
