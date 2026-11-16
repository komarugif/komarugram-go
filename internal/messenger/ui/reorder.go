// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"slices"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// reorderRows is the drag that puts rows of one height in another order,
// by handles beside them, as the settings' menus and the account's
// playlist have it: the row taken follows the pointer, the others slide
// into their new places, and the row let go settles into its own. Rows
// are told apart by IDs.
type reorderRows struct {
	// tag takes the presses on the handles.
	tag      byte
	dragging string
	pointer  pointer.ID
	// order is the order while a row is taken, which a drop keeps.
	order []string
	// top is where the rows start, as the last frame drew them.
	top          int
	grabY, dragY float32
	positions    map[string]*menuRowMotion
}

// update takes the drag over the rows of ids, height px high each. It
// reports the order a row was let go in, ids moved, when one was.
func (r *reorderRows) update(gtx layout.Context, ids []string, height int) ([]string, bool) {
	if r.dragging != "" && !slices.Contains(ids, r.dragging) {
		r.cancel()
	}
	var dropped []string
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &r.tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		h := float32(height)
		localY := e.Position.Y - float32(r.top)
		index := int(localY / h)
		switch e.Kind {
		case pointer.Press:
			if (e.Source != pointer.Mouse || e.Buttons == pointer.ButtonPrimary) && index >= 0 && index < len(ids) {
				// Rows may still be settling after the previous drop. Hit the
				// visible row rather than interpreting its old slot as another item.
				distance := float32(math.MaxFloat32)
				for i, id := range ids {
					if row := r.positions[id]; row != nil {
						d := float32(math.Abs(float64(localY - row.value - h/2)))
						if d < distance {
							index = i
							distance = d
						}
					}
				}
				r.dragging = ids[index]
				r.pointer = e.PointerID
				r.order = slices.Clone(ids)
				r.dragY = float32(index) * h
				if row := r.positions[r.dragging]; row != nil {
					r.dragY = row.value
				}
				r.grabY = localY - r.dragY
				gtx.Execute(pointer.GrabCmd{Tag: &r.tag, ID: e.PointerID})
			}
		case pointer.Drag:
			if r.dragging != "" && e.PointerID == r.pointer {
				r.dragY = max(0, min(localY-r.grabY, float32(len(r.order)-1)*h))
				r.order = moveMenuItem(r.order, r.dragging, int((r.dragY+h/2)/h))
				gtx.Execute(op.InvalidateCmd{})
			}
		case pointer.Release:
			if r.dragging != "" && e.PointerID == r.pointer {
				dropped = r.order
				r.cancel()
				gtx.Execute(op.InvalidateCmd{})
			}
		case pointer.Cancel:
			r.cancel()
		}
	}
	return dropped, dropped != nil
}

// cancel lets the row taken go where it was.
func (r *reorderRows) cancel() {
	r.dragging = ""
	r.order = nil
}

// shown is ids in the order they are drawn: the drag's while a row is
// taken.
func (r *reorderRows) shown(ids []string) []string {
	if r.dragging != "" {
		return r.order
	}
	return ids
}

// drawOrder is shown with the row taken last, to be drawn over the others.
func (r *reorderRows) drawOrder(shown []string) []string {
	if r.dragging == "" {
		return shown
	}
	out := slices.DeleteFunc(slices.Clone(shown), func(id string) bool { return id == r.dragging })
	return append(out, r.dragging)
}

// rowY is where the row of id, at place i of shown, is drawn, from where
// the rows start, height px high each.
func (r *reorderRows) rowY(gtx layout.Context, id string, i, height int) int {
	if r.positions == nil {
		r.positions = map[string]*menuRowMotion{}
	}
	motion := r.positions[id]
	if motion == nil {
		motion = new(menuRowMotion)
		r.positions[id] = motion
	}
	target := float32(i * height)
	if r.dragging == id {
		target = r.dragY
	}
	return int(math.Round(float64(motion.Position(gtx, target, r.dragging == id))))
}

// handle lays out where a press takes a row: rect, in the rows'
// coordinates, whose start is top in the frame's.
func (r *reorderRows) handle(gtx layout.Context, rect image.Rectangle) {
	defer clip.Rect(rect).Push(gtx.Ops).Pop()
	pointer.CursorGrab.Add(gtx.Ops)
	event.Op(gtx.Ops, &r.tag)
}
