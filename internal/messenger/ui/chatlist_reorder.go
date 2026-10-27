// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"math"
	"slices"
	"sync"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// The pinned chats of the list of all chats are put in another order by
// dragging them, as in Telegram Desktop (dialogs_inner_widget.cpp,
// updateReorderPinned): a press on a pinned chat that goes up or down by
// reorderThreshold, or is held for reorderHold, lifts its row. The row
// follows the pointer among the pinned ones, which slide out of its way as
// it passes half of them, and where it is let go it slides into its place
// and the order is saved (savePinnedOrder), unless a chat was pinned or
// unpinned meanwhile. A touch lifts the row only by the hold: its drag
// scrolls the list. Folders have no pins of their own here (see
// chatRowMenu.actions), so only the list of all chats is reordered.

const (
	// reorderThreshold is how far a mouse press on a pinned chat goes up
	// or down before its row is lifted: kStartReorderThreshold.
	reorderThreshold = unit.Dp(30)
	// reorderHold is how long a press held in place lifts the row.
	reorderHold = 400 * time.Millisecond
	// reorderSlop is how far a touch may move and still be held.
	reorderSlop = unit.Dp(8)
	// reorderSlide is how long the rows slide to their places:
	// st::stickersRowDuration.
	reorderSlide = 200 * time.Millisecond
)

// rowSlide is a row sliding to its place from an offset.
type rowSlide struct {
	from  float32
	start time.Time
}

// pinReorder drags the pinned chats of the list into another order.
type pinReorder struct {
	// store saves the order; nil leaves the rows in place.
	store model.PinReorderer
	// invalidate redraws the window when a save is answered.
	invalidate func()

	// area takes the pointer over the list, rows the presses on the
	// pinned chats, to know which was pressed.
	area byte
	rows map[int64]*byte

	// pressed is the pinned chat pressed by pointer, 0 for none: from
	// source, at start in the list and at since; at is where the pointer
	// is now.
	pressed   int64
	pointer   pointer.ID
	source    pointer.Source
	start, at f32.Point
	since     time.Time
	// lifted is the chat dragged, or sliding back into its place after it
	// was let go; dragging tells whether it is held still.
	lifted   int64
	dragging bool
	// order is the pinned chats in the order they are dragged to, which
	// the list shows until the store has it; nil for the store's. from is
	// where lifted was in it when it was lifted.
	order []int64
	from  int
	// stale counts the frames the store, done saving, has shown another
	// order: one it was told meanwhile is the list's after a while.
	stale  int
	slides map[int64]rowSlide
	// drawn is the chat the last frame drew lifted, 0 for none.
	drawn int64

	mu sync.Mutex
	// saves are the orders being saved; refused tells that one was not
	// kept, failed why, for the toast, until it is told.
	saves   int
	refused bool
	failed  error
}

// row is the tag that takes the presses on the row of chat.
func (r *pinReorder) row(chat int64) *byte {
	if r.rows == nil {
		r.rows = map[int64]*byte{}
	}
	t := r.rows[chat]
	if t == nil {
		t = new(byte)
		r.rows[chat] = t
	}
	return t
}

// update follows the pointer over the list of sec, whose chats are those of
// the store, and shown those laid out in the last frame. It returns chats
// in the order the list shows.
func (r *pinReorder) update(gtx layout.Context, sec section, chats []model.Chat, shown []int64, toast *toast, l localization.Catalog) []model.Chat {
	if r.store == nil {
		return chats
	}
	r.settle(chats, toast, l)
	if sec.kind != sectionAll {
		r.pressed, r.dragging = 0, false
	}
	var hit int64
	for _, id := range shown {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: r.row(id), Kinds: pointer.Press})
			if !ok {
				break
			}
			if e, ok := ev.(pointer.Event); ok && (e.Source == pointer.Touch || e.Buttons == pointer.ButtonPrimary) {
				hit = id
			}
		}
	}
	arranged := r.arrange(chats)
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &r.area, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			if r.pressed != 0 || r.dragging || hit == 0 || sec.kind != sectionAll || model.PinnedCount(arranged) < 2 {
				continue
			}
			r.pressed, r.pointer, r.source = hit, e.PointerID, e.Source
			r.start, r.at, r.since = e.Position, e.Position, gtx.Now
		case pointer.Drag:
			if r.pressed == 0 || e.PointerID != r.pointer {
				continue
			}
			r.at = e.Position
			if r.dragging {
				continue
			}
			moved := r.at.Sub(r.start)
			switch {
			case r.source == pointer.Mouse && abs32(moved.Y) >= float32(gtx.Dp(reorderThreshold)):
				r.lift(gtx, arranged)
			case r.source != pointer.Mouse && max(abs32(moved.X), abs32(moved.Y)) > float32(gtx.Dp(reorderSlop)):
				// The touch scrolls the list.
				r.pressed = 0
			}
		case pointer.Release, pointer.Cancel:
			if r.pressed == 0 || e.PointerID != r.pointer {
				continue
			}
			if r.dragging {
				// Where it is let go, though no move told of it: a
				// pointer that jumps there sends none.
				if e.Kind == pointer.Release {
					r.at = e.Position
					r.move(gtx)
				}
				r.drop(gtx, chats)
			}
			r.pressed = 0
		}
	}
	if r.pressed != 0 && !r.dragging {
		if at := r.since.Add(reorderHold); gtx.Now.Before(at) {
			gtx.Execute(op.InvalidateCmd{At: at})
		} else {
			r.lift(gtx, arranged)
		}
	}
	if r.dragging {
		r.move(gtx)
	}
	return r.arrange(chats)
}

// settle tells what the saves came to, and gives the list back the
// store's order once it has the one dragged to, or kept another.
func (r *pinReorder) settle(chats []model.Chat, toast *toast, l localization.Catalog) {
	r.mu.Lock()
	saves, refused, failed := r.saves, r.refused, r.failed
	r.failed = nil
	if saves == 0 {
		r.refused = false
	}
	r.mu.Unlock()
	if failed != nil {
		toast.Show(l.T("chat_row.pin_failed"))
	}
	if r.order == nil {
		return
	}
	if !model.SamePinned(chats, r.order) {
		// A chat was pinned or unpinned meanwhile: the drag is over.
		r.order, r.pressed, r.dragging, r.lifted, r.stale = nil, 0, false, 0, 0
		return
	}
	if r.dragging || saves > 0 {
		return
	}
	if refused || slices.Equal(model.PinnedOrder(chats), r.order) || r.stale >= 2 {
		r.order, r.stale = nil, 0
		return
	}
	r.stale++
}

// arrange returns chats with the pinned ones in the order dragged to.
func (r *pinReorder) arrange(chats []model.Chat) []model.Chat {
	if r.order == nil || !model.SamePinned(chats, r.order) {
		return chats
	}
	return model.WithPinOrder(chats, r.order)
}

// lift lifts the row of the chat pressed, of the chats the list shows, and
// takes the pointer from the rows and the scroll under it.
func (r *pinReorder) lift(gtx layout.Context, chats []model.Chat) {
	order := model.PinnedOrder(chats)
	i := slices.Index(order, r.pressed)
	if i < 0 || len(order) < 2 {
		r.pressed = 0
		return
	}
	r.order, r.from, r.lifted, r.dragging, r.stale = order, i, r.pressed, true, 0
	gtx.Execute(pointer.GrabCmd{Tag: &r.area, ID: r.pointer})
	gtx.Execute(op.InvalidateCmd{})
}

// move puts the lifted chat where the pointer took it among the pinned, and
// has those it passed slide out of its way.
func (r *pinReorder) move(gtx layout.Context) {
	height := float32(gtx.Dp(chatRowHeight))
	cur := slices.Index(r.order, r.lifted)
	n := len(r.order)
	target := r.from + int(math.Round(float64((r.at.Y-r.start.Y)/height)))
	target = min(max(target, 0), n-1)
	// A row passed is drawn a place further, from where it was.
	for ; cur > target; cur-- {
		other := r.order[cur-1]
		r.order[cur] = other
		r.slide(other, r.offset(other, gtx.Now)-height, gtx.Now)
	}
	for ; cur < target; cur++ {
		other := r.order[cur+1]
		r.order[cur] = other
		r.slide(other, r.offset(other, gtx.Now)+height, gtx.Now)
	}
	r.order[cur] = r.lifted
}

// liftedOffset is how far the lifted row is drawn from its place.
func (r *pinReorder) liftedOffset(gtx layout.Context) float32 {
	if !r.dragging {
		return r.offset(r.lifted, gtx.Now)
	}
	height := float32(gtx.Dp(chatRowHeight))
	cur := slices.Index(r.order, r.lifted)
	y := r.at.Y - r.start.Y - float32(cur-r.from)*height
	// It stays among the pinned.
	return min(max(y, -float32(cur)*height), float32(len(r.order)-1-cur)*height)
}

// drop lets the lifted chat go: it slides into its place, and the order is
// saved, if it is another than the store's chats have.
func (r *pinReorder) drop(gtx layout.Context, chats []model.Chat) {
	r.slide(r.lifted, r.liftedOffset(gtx), gtx.Now)
	r.dragging = false
	if r.order == nil || slices.Equal(model.PinnedOrder(chats), r.order) {
		return
	}
	order := slices.Clone(r.order)
	r.mu.Lock()
	r.saves++
	r.mu.Unlock()
	go func() {
		defer crash.Recover("reorder pinned chats", func(e *crash.Panic) { r.saved(e) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r.saved(r.store.ReorderPinned(ctx, order))
	}()
}

// saved takes what a save came to.
func (r *pinReorder) saved(err error) {
	r.mu.Lock()
	r.saves--
	if err != nil {
		r.refused = true
		// Pins changed meanwhile are the list's to show, not a failure.
		if !errors.Is(err, model.ErrPinnedChanged) {
			r.failed = err
		}
	}
	r.mu.Unlock()
	if r.invalidate != nil {
		r.invalidate()
	}
}

func (r *pinReorder) slide(id int64, from float32, now time.Time) {
	if r.slides == nil {
		r.slides = map[int64]rowSlide{}
	}
	r.slides[id] = rowSlide{from: from, start: now}
}

// offset is how far the row of id is drawn from its place at now, as it
// slides into it.
func (r *pinReorder) offset(id int64, now time.Time) float32 {
	s, ok := r.slides[id]
	if !ok {
		return 0
	}
	t := float64(now.Sub(s.start)) / float64(reorderSlide)
	if t >= 1 || t < 0 {
		delete(r.slides, id)
		return 0
	}
	// anim::sineInOut.
	return s.from * float32(1-(0.5-math.Cos(math.Pi*t)/2))
}

// animate keeps the frames coming while rows slide, and forgets the lifted
// chat once it is in its place. The slides over are forgotten first: a
// lifted chat whose slide ends in this frame is set down in it, as no
// other frame may come to do it.
func (r *pinReorder) animate(gtx layout.Context) {
	for id := range r.slides {
		r.offset(id, gtx.Now)
	}
	if r.lifted != 0 && !r.dragging {
		if _, ok := r.slides[r.lifted]; !ok {
			r.lifted = 0
			// This frame drew it lifted: the next one draws it in its
			// place, and no other may come.
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	if len(r.slides) > 0 {
		gtx.Execute(op.InvalidateCmd{})
	}
}

// areaOp makes the list take the pointer for where it goes; the presses go
// on to the rows under it. It is laid out over them.
func (r *pinReorder) areaOp(gtx layout.Context) {
	if r.store == nil {
		return
	}
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &r.area)
}

// rowOp makes the row of chat, of size, take presses.
func (r *pinReorder) rowOp(gtx layout.Context, chat int64, size image.Point) {
	if r.store == nil {
		return
	}
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, r.row(chat))
}

// layoutLifted draws the lifted row over the list, at top of its place.
func (r *pinReorder) layoutLifted(gtx layout.Context, top int, row layout.Widget) {
	r.drawn = r.lifted
	y := top + int(math.Round(float64(r.liftedOffset(gtx))))
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	defer op.Offset(image.Pt(0, y)).Push(gtx.Ops).Pop()
	margin := gtx.Dp(chatRowMargin)
	rect := image.Rect(margin, 0, gtx.Constraints.Max.X-margin, gtx.Dp(chatRowHeight))
	radius := gtx.Dp(chatSelectRadius)
	drawShadow(gtx, rect, radius, menuShadow)
	paint.FillShape(gtx.Ops, scheme(gtx).SurfaceContainerHigh.AsNRGBA(), clip.UniformRRect(rect, radius).Op(gtx.Ops))
	row(gtx)
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
