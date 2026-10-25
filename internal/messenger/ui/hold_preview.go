// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/model"
)

// holdPreviewDelay is how long an emoji, a sticker or a GIF is held before
// it shows large, as Telegram Desktop's panel waits QApplication's
// startDragTime.
const holdPreviewDelay = 500 * time.Millisecond

// holdPreview shows an item of a grid of emoji, stickers or GIFs large over
// the whole window while the mouse button is held on it, as Telegram
// Desktop's MediaPreviewWidget: the panel of the composer and the box of a
// set. It takes no pointer: moved over the grid with the button held, it
// shows the item under it, and released, it closes, and the release
// chooses nothing.
//
// A grid lays out its rows in a layout.List and tells each row's height
// (row) and each item's place in it (tile); area, laid out over the grid
// after it, reads the pointer passing through and places the tiles by
// the list's position. Gio tells no item of the pointer over it while the
// button is held on another, so the grid's own geometry finds it.
type holdPreview struct {
	// pressed is set while the button is held, since at, at the point
	// at, in the area's coordinates; active once it shows.
	pressed, active bool
	since           time.Time
	at              f32.Point
	// swallow is set in the frame of the release that closed it: the
	// click of the tile under it chooses nothing.
	swallow bool
	// menu is the tile right-clicked in this frame, at menuAt, -1 for
	// none: the grid opens its menu.
	menu   int
	menuAt f32.Point
	// item is what it shows.
	item model.PickerItem
	gif  bool
	// tiles are the items the grid placed in the last frame, in the
	// area's coordinates; rows and building are this frame's, in the
	// list's rows.
	tiles    []holdTile
	building []holdTile
	rows     map[int]int
}

type holdTile struct {
	row  int
	rect image.Rectangle
	item model.PickerItem
	gif  bool
}

// Update takes the pointer's events over the area; call it at the top of
// the grid's layout, before the tiles take their clicks.
func (h *holdPreview) Update(gtx layout.Context) {
	h.swallow = false
	h.menu = -1
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: h, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			if e.Source == pointer.Mouse && e.Buttons == pointer.ButtonPrimary {
				h.pressed, h.active, h.since, h.at = true, false, gtx.Now, e.Position
				gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(holdPreviewDelay)})
			}
			if e.Source == pointer.Mouse && e.Buttons == pointer.ButtonSecondary && !h.pressed {
				h.menu, h.menuAt = h.tileAt(e.Position), e.Position
			}
		case pointer.Drag:
			if h.pressed {
				h.at = e.Position
			}
		case pointer.Release, pointer.Cancel:
			h.swallow = h.active
			h.pressed, h.active = false, false
		}
	}
	if h.pressed && !h.active && !gtx.Now.Before(h.since.Add(holdPreviewDelay)) {
		h.active = h.under() >= 0
	}
	if h.active {
		if i := h.under(); i >= 0 {
			h.item, h.gif = h.tiles[i].item, h.tiles[i].gif
		}
	}
	h.building = h.building[:0]
	clear(h.rows)
}

// under is the tile under the pointer, -1 for none.
func (h *holdPreview) under() int { return h.tileAt(h.at) }

// tileAt is the tile at pos, -1 for none.
func (h *holdPreview) tileAt(pos f32.Point) int {
	p := image.Pt(int(pos.X), int(pos.Y))
	for i, t := range h.tiles {
		if p.In(t.rect) {
			return i
		}
	}
	return -1
}

// Menu is the tile right-clicked in this frame, where it was in the area's
// coordinates, if any.
func (h *holdPreview) Menu() (holdTile, image.Point, bool) {
	if h.menu < 0 || h.menu >= len(h.tiles) {
		return holdTile{}, image.Point{}, false
	}
	return h.tiles[h.menu], image.Pt(int(h.menuAt.X), int(h.menuAt.Y)), true
}

// Swallow reports whether a click of this frame closed the preview, which
// chooses nothing.
func (h *holdPreview) Swallow() bool { return h.swallow }

// row tells that row n of the list is height high.
func (h *holdPreview) row(n, height int) {
	if h.rows == nil {
		h.rows = map[int]int{}
	}
	h.rows[n] = height
}

// tile tells that row n holds item at rect, in the row's coordinates.
func (h *holdPreview) tile(n int, rect image.Rectangle, item model.PickerItem, gif bool) {
	h.building = append(h.building, holdTile{row: n, rect: rect, item: item, gif: gif})
}

// area lays out, over the grid of size, the area reading the pointer, and
// places the tiles told this frame by list, laid out at origin of it: the
// list's first row is at its offset, the others after it.
func (h *holdPreview) area(gtx layout.Context, size image.Point, list *layout.List, origin image.Point) {
	tops := map[int]int{}
	y := origin.Y - list.Position.Offset
	for n := list.Position.First; ; n++ {
		height, ok := h.rows[n]
		if !ok {
			break
		}
		tops[n] = y
		y += height
	}
	h.tiles = h.tiles[:0]
	for _, t := range h.building {
		top, ok := tops[t.row]
		if !ok {
			continue
		}
		h.tiles = append(h.tiles, holdTile{row: t.row, rect: t.rect.Add(image.Pt(origin.X, top)), item: t.item, gif: t.gif})
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	// The tiles under it take the same press, and their clicks.
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, h)
}

// Shown reports whether it shows an item.
func (h *holdPreview) Shown() bool { return h.active }

// layoutHoldPreview draws over the whole window the item of the composer's
// picker or of the box of a set held, if any.
func (p *chatPage) layoutHoldPreview(gtx layout.Context) {
	if p == nil {
		return
	}
	if p.composer != nil && p.composer.preview.Shown() {
		p.composer.preview.Layout(gtx, p)
	}
	if p.stickers.preview.Shown() {
		p.stickers.preview.Layout(gtx, p)
	}
}

// Layout draws the item shown over the whole window, on the viewer's
// backdrop, taking no pointer: what is under it goes on taking the moves.
func (h *holdPreview) Layout(gtx layout.Context, p *chatPage) {
	if !h.active {
		return
	}
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, viewerBackdrop, clip.Rect{Max: size}.Op())
	if p == nil || p.media == nil || p.images == nil {
		return
	}
	item := h.item
	// A sticker and an emoji show as Telegram Desktop shows them, a GIF
	// as large as most of the window allows.
	box := min(gtx.Dp(unit.Dp(320)), min(size.X, size.Y)*3/5)
	target := image.Pt(box, box)
	if h.gif {
		target = image.Pt(size.X*7/10, size.Y*7/10)
	}
	if item.Media.Media == nil {
		// An emoji of the fonts, shaped at the box's size: a glyph scaled
		// up from the text's would show its pixels.
		lgtx := gtx
		lgtx.Constraints = layout.Constraints{Max: image.Pt(1<<20, size.Y)}
		macro := op.Record(gtx.Ops)
		dims := label(lgtx, item.Emoji, token.TypestyleDisplayLarge, token.NewMatColorFromHexRGB(0xffffff), 1)
		macro.Stop()
		lgtx.Metric.PxPerSp *= float32(box) / float32(max(dims.Size.X, dims.Size.Y, 1))
		macro = op.Record(gtx.Ops)
		dims = label(lgtx, item.Emoji, token.TypestyleDisplayLarge, token.NewMatColorFromHexRGB(0xffffff), 1)
		call := macro.Stop()
		offset(gtx, size.Sub(dims.Size).Div(2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		return
	}
	status := p.media.StatusFit(item.Media, true, target, false)
	im := status.Frame
	if im == nil {
		im = status.Preview
	}
	if im == nil {
		return
	}
	if item.Media.Media.TextColor {
		im = p.images.Colored(im, token.NewMatColorFromHexRGB(0xffffff).AsNRGBA())
	}
	// Fitted into the target, its own shape kept.
	b := im.Bounds()
	fit := min(float32(target.X)/float32(max(b.Dx(), 1)), float32(target.Y)/float32(max(b.Dy(), 1)))
	shown := image.Pt(int(float32(b.Dx())*fit), int(float32(b.Dy())*fit))
	offset(gtx, size.Sub(shown).Div(2), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(shown)
		if h.gif {
			defer clip.UniformRRect(image.Rectangle{Max: shown}, gtx.Dp(12)).Push(gtx.Ops).Pop()
		}
		return widget.Image{Src: p.images.Op(im), Fit: widget.Contain}.Layout(gtx)
	})
	if item.Emoji != "" && !h.gif {
		// The sticker's emoji over it, as Telegram Desktop's.
		lgtx := gtx
		lgtx.Constraints = layout.Constraints{Max: size}
		macro := op.Record(gtx.Ops)
		dims := label(lgtx, item.Emoji, token.TypestyleHeadlineMedium, token.NewMatColorFromHexRGB(0xffffff), 1)
		call := macro.Stop()
		top := (size.Y-shown.Y)/2 - gtx.Dp(12) - dims.Size.Y
		offset(gtx, image.Pt((size.X-dims.Size.X)/2, max(0, top)), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
	}
}
