// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	// storiesRowHeight is the height of the row of stories over the chats,
	// storyItem the width of one in it, storyAvatar its avatar's side.
	storiesRowHeight = unit.Dp(92)
	storyItem        = unit.Dp(78)
	storyAvatar      = unit.Dp(52)
	// storyRingGap is the space between the avatar and its ring, which
	// is storyRing thick for stories unread and storyRingRead for those
	// seen, as Telegram Desktop's lineTwice and lineReadTwice.
	storyRingGap  = unit.Dp(3)
	storyRing     = unit.Dp(2)
	storyRingRead = unit.Dp(1)
)

// storiesRow is the row of stories over the list of chats, as Telegram
// Desktop's Dialogs::Stories::List draws it: the avatar of each who has
// stories in a ring of a segment a story, those unread in the accent's
// gradient and those seen dimmed, and the name under it. It is the list's
// first row, and goes up with the chats, not staying over them.
type storiesRow struct {
	list  layout.List
	drag  stripDrag
	items map[int64]*widget.Clickable
	// shown are the peers laid out in the last frame.
	shown []model.StoryPeer
}

// clicked is the peer of a story clicked since the last frame.
func (r *storiesRow) clicked(gtx layout.Context) (model.StoryPeer, bool) {
	for _, p := range r.shown {
		if c := r.items[p.ID]; c != nil && c.Clicked(gtx) {
			return p, true
		}
	}
	return model.StoryPeer{}, false
}

// layout draws the row of peers in the width of the constraints.
func (r *storiesRow) layout(gtx layout.Context, peers []model.StoryPeer, avatarOf avatarLayout, catalog localization.Catalog, invalidate func()) layout.Dimensions {
	if r.items == nil {
		r.items = map[int64]*widget.Clickable{}
		r.list.Axis = layout.Horizontal
	}
	r.shown = append(r.shown[:0], peers...)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(storiesRowHeight))
	gtx.Constraints = layout.Exact(size)
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	inset := gtx.Dp(4)
	offset(gtx, image.Pt(inset, 0), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(max(size.X-2*inset, 0), size.Y))
		dims := r.list.Layout(gtx, len(peers), func(gtx layout.Context, i int) layout.Dimensions {
			return r.layoutPeer(gtx, peers[i], avatarOf, catalog)
		})
		dragOnlyHorizontalStrip(gtx, gtx.Constraints.Max.X, &r.drag, &r.list, invalidate)
		return dims
	})
	return layout.Dimensions{Size: size}
}

// layoutPeer draws p: its avatar in its ring, and its name.
func (r *storiesRow) layoutPeer(gtx layout.Context, p model.StoryPeer, avatarOf avatarLayout, catalog localization.Catalog) layout.Dimensions {
	click := r.items[p.ID]
	if click == nil {
		click = new(widget.Clickable)
		r.items[p.ID] = click
	}
	sc := scheme(gtx)
	size := image.Pt(gtx.Dp(storyItem), gtx.Constraints.Max.Y)
	gtx.Constraints = layout.Exact(size)
	title := p.Title
	if p.Self {
		title = catalog.T("stories.my")
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		if click.Hovered() {
			fillRounded(gtx, sc.Surface.OnColor.SetOpacity(.06), size, gtx.Dp(12))
		}
		avatarPx := gtx.Dp(storyAvatar)
		ring := avatarPx + 2*gtx.Dp(storyRingGap) + 2*gtx.Dp(storyRing)
		top := gtx.Dp(8)
		ringAt := image.Pt((size.X-ring)/2, top)
		offset(gtx, ringAt, func(gtx layout.Context) layout.Dimensions {
			drawStoryRing(gtx, ring, p.Count, p.Unread, sc.Primary.Color, sc.Tertiary.Color, sc.OutlineVariant)
			return layout.Dimensions{Size: image.Pt(ring, ring)}
		})
		offset(gtx, ringAt.Add(image.Pt((ring-avatarPx)/2, (ring-avatarPx)/2)), func(gtx layout.Context) layout.Dimensions {
			// The account's own is its avatar; Saved Messages' mark is not.
			kind := p.Kind
			if avatarOf != nil {
				return avatarOf(gtx, p.ID, kind, p.Title, storyAvatar)
			}
			return avatar(gtx, p.ID, kind, p.Title, storyAvatar)
		})
		col := sc.Surface.OnColor
		if p.Unread == 0 {
			col = sc.SurfaceVariant.OnColor
		}
		nameGtx := gtx
		nameGtx.Constraints = layout.Constraints{Max: image.Pt(size.X-gtx.Dp(2), size.Y)}
		macro := op.Record(gtx.Ops)
		dims := label(nameGtx, title, token.TypestyleLabelSmall, col, 1)
		call := macro.Stop()
		offset(gtx, image.Pt((size.X-dims.Size.X)/2, top+ring+gtx.Dp(4)), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		return layout.Dimensions{Size: size}
	})
}

// drawStoryRing draws the ring of count stories in a square of side d,
// from the top clockwise: the seen first, dimmed and thin, then the unread
// in the gradient of from and to, as Telegram Desktop's segments. One
// story is a whole circle.
func drawStoryRing(gtx layout.Context, d, count, unread int, from, to, read token.MatColor) {
	if count <= 0 {
		return
	}
	thick, thin := float32(gtx.Dp(storyRing)), float32(gtx.Dp(storyRingRead))
	c := f32.Pt(float32(d)/2, float32(d)/2)
	outer := float32(d) / 2
	radius := outer - thick/2
	// The gap between segments, an angle; none for one story.
	gap := float32(0)
	if count > 1 {
		gap = min(float32(math.Pi)/float32(count)/2, float32(gtx.Dp(4))/radius)
	}
	sweep := 2*float32(math.Pi)/float32(count) - gap
	for k := range count {
		start := -float32(math.Pi)/2 + float32(k)*2*float32(math.Pi)/float32(count) + gap/2
		seen := k < count-unread
		width := thick
		if seen {
			width = thin
		}
		// A band between two arcs, filled: a stroked arc leaves a faint
		// trace over the square it is in.
		point := func(r, angle float32) f32.Point {
			return f32.Pt(c.X+r*float32(math.Cos(float64(angle))), c.Y+r*float32(math.Sin(float64(angle))))
		}
		inner := outer - width
		var path clip.Path
		path.Begin(gtx.Ops)
		at := point(outer, start)
		path.MoveTo(at)
		path.Arc(c.Sub(at), c.Sub(at), sweep)
		at = point(inner, start+sweep)
		path.LineTo(at)
		path.Arc(c.Sub(at), c.Sub(at), -sweep)
		path.Close()
		stack := clip.Outline{Path: path.End()}.Op().Push(gtx.Ops)
		if seen {
			paint.ColorOp{Color: read.AsNRGBA()}.Add(gtx.Ops)
		} else {
			paint.LinearGradientOp{Stop1: f32.Pt(float32(d), 0), Color1: from.AsNRGBA(), Stop2: f32.Pt(0, float32(d)), Color2: to.AsNRGBA()}.Add(gtx.Ops)
		}
		paint.PaintOp{}.Add(gtx.Ops)
		stack.Pop()
	}
}
