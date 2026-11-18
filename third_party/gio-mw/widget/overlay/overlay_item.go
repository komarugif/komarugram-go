// SPDX-License-Identifier: Unlicense OR MIT

package overlay

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"image"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

var itemId int64

type Item struct {
	// closed items play their exit animation, then removed is set and the
	// overlay drops them.
	closed     bool
	removed    bool
	visibility wdk.FloatTween // 0 hidden, 1 fully shown.
	lGravity   block.Gravity
	lWidget    layout.Widget
	showScrim  bool
	scrimClose bool
	tDuration  time.Duration
	tDisplayed time.Time
	id         int64
	iClickable *widget.Clickable
}

func NewItem(lWidget layout.Widget, lGravity block.Gravity) *Item {
	itemId = itemId + 1
	return &Item{
		lWidget:    lWidget,
		lGravity:   lGravity,
		id:         itemId,
		iClickable: &widget.Clickable{},
	}
}

func (i *Item) WithDuration(t time.Duration) *Item {
	if t < 0 {
		panic("duration must be positive")
	}
	i.tDuration = t
	return i
}

func (i *Item) WithScrim() *Item {
	i.showScrim = true
	return i
}

// CloseOnScrim closes the item when its scrim is clicked. The item's widget
// must then take the clicks on its own area, or they reach the scrim too.
func (i *Item) CloseOnScrim() *Item {
	i.showScrim = true
	i.scrimClose = true
	return i
}

func (i *Item) GetId() int64 {
	return i.id
}

func (i *Item) Close() {
	i.closed = true
}

// Closed reports whether the item was closed, by Close or by the user; it
// may still be animating out.
func (i *Item) Closed() bool {
	return i.closed
}

func (i *Item) update(gtx layout.Context, topMost bool) {
	if i.closed {
		return
	}
	// The item was just added to the stack, initialize its display time.
	if i.tDisplayed.IsZero() {
		i.tDisplayed = gtx.Now
	}
	if i.tDuration > 0 {
		// Close the item once its display duration has expired. Otherwise
		// schedule a frame for that moment; this is repeated every frame
		// because each frame replaces the previously scheduled one.
		deadline := i.tDisplayed.Add(i.tDuration)
		if !gtx.Now.Before(deadline) {
			i.closed = true
		} else {
			gtx.Execute(op.InvalidateCmd{At: deadline})
		}
	}
	if topMost {
		if i.iClickable.Clicked(gtx) && i.scrimClose {
			i.closed = true
		}
		for {
			ev, ok := gtx.Event(
				key.Filter{Name: key.NameEscape},
			)
			if !ok {
				break
			}
			switch tEv := ev.(type) {
			case key.Event:
				if tEv.State == key.Release && tEv.Name == key.NameEscape {
					i.closed = true
				}
			}
		}
	}
}

// Enter and exit animations, modeled on Material motion for dialogs, sheets
// and snackbars.
const (
	enterDuration = token.DurationMedium2
	exitDuration  = token.DurationShort4
	slideDistance = 24 // Dp, for items at the top or bottom center.
	enterScale    = 0.9
)

func (i *Item) animateVisibility(gtx layout.Context) float32 {
	if i.visibility.Duration == 0 {
		// Start hidden, so the item animates in on its first frame.
		i.visibility.AnimateFor(gtx, wdk.AnimationMenu, 0)
	}
	target := float32(1)
	i.visibility.Duration = enterDuration
	i.visibility.Easing = &token.EasingEmphasizedDecelerate
	if i.closed {
		target = 0
		i.visibility.Duration = exitDuration
		i.visibility.Easing = &token.EasingEmphasizedAccelerate
	}
	v := i.visibility.AnimateFor(gtx, wdk.AnimationMenu, target)
	if i.closed && v == 0 {
		i.removed = true
	}
	return v
}

func (i *Item) layout(gtx layout.Context, oTheme *Theme) layout.Dimensions {
	visibility := i.animateVisibility(gtx)
	if i.closed {
		// A closing item ignores input while it animates out.
		gtx = gtx.Disabled()
	}
	if i.removed {
		return layout.Dimensions{}
	}
	if i.showScrim {
		i.iClickable.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			scrimColor := oTheme.EnabledScrimColor.SetOpacity(oTheme.EnabledScrimOpacity)
			scrimColor.A = uint8(float32(scrimColor.A)*visibility + 0.5)
			paint.Fill(gtx.Ops, scrimColor.AsNRGBA())
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
	}

	return block.Container{
		Gravity: i.lGravity,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if visibility == 1 {
			return i.lWidget(gtx)
		}
		macroOp := op.Record(gtx.Ops)
		dims := i.lWidget(gtx)
		content := macroOp.Stop()
		i.drawTransition(gtx, content, dims.Size, visibility)
		return dims
	})
}

// drawTransition draws the item content part way in, depending on where the
// item sits: side items slide in from their edge, centered items grow and
// fade in, top and bottom items slide a little and fade in.
func (i *Item) drawTransition(gtx layout.Context, content op.CallOp, size image.Point, visibility float32) {
	hidden := 1 - visibility
	sz := layout.FPt(size)
	transform := f32.AffineId()
	fade := true
	fromStart := func() float32 {
		if gtx.Locale.Direction == system.RTL {
			return 1
		}
		return -1
	}
	switch i.lGravity {
	case block.GravityTopStart, block.GravityMiddleStart, block.GravityBottomStart:
		transform = transform.Offset(f32.Pt(fromStart()*sz.X*hidden, 0))
		fade = false
	case block.GravityTopEnd, block.GravityMiddleEnd, block.GravityBottomEnd:
		transform = transform.Offset(f32.Pt(-fromStart()*sz.X*hidden, 0))
		fade = false
	case block.GravityTopCenter:
		transform = transform.Offset(f32.Pt(0, -float32(gtx.Dp(slideDistance))*hidden))
	case block.GravityBottomCenter:
		transform = transform.Offset(f32.Pt(0, float32(gtx.Dp(slideDistance))*hidden))
	default:
		scale := enterScale + (1-enterScale)*visibility
		transform = transform.Scale(sz.Mul(0.5), f32.Pt(scale, scale))
	}
	transformStack := op.Affine(transform).Push(gtx.Ops)
	if fade {
		opacityStack := paint.PushOpacity(gtx.Ops, visibility)
		content.Add(gtx.Ops)
		opacityStack.Pop()
	} else {
		content.Add(gtx.Ops)
	}
	transformStack.Pop()
}
