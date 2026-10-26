// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strconv"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/slider"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// sharedDrag selects the files of a section by a drag across them. In
// order, as Telegram Desktop's shared media does (_dragSelectFrom,
// _dragSelectTo), what is selected is the range from the file pressed to
// the one under the pointer, in the section's order: however fast the
// pointer moves, none between is left out. After the cursor, it is the
// files the pointer passed over: those the segment between one of its
// events and the next crosses, the limit of events made between the two
// at ever smaller steps, so that a fast move, of few events far apart,
// leaves none it crossed out, a corner of one either. A drag selects only
// once a file was selected by its menu, for one not to select by chance:
// then a press on a file selects or unselects it, and a drag goes on from
// there.
type sharedDrag struct {
	tag byte
	// dragging is set while a drag selects; origin is where it was
	// pressed.
	dragging         bool
	pointer          pointer.ID
	origin, position f32.Point
	// anchor is the index of the file pressed; selecting tells whether the
	// range selects or unselects, as the anchor was not or was selected;
	// before is the selection the drag started from.
	anchor    int
	selecting bool
	before    map[model.MessageID]bool
	scrollAt  time.Time
	// cursor is set for a drag that selects after the cursor, from last,
	// where its pointer was at the event before.
	cursor bool
	last   f32.Point
}

// selectModeMenu chooses how a drag selects: in order, or after the
// cursor. button, in the header, opens it.
type selectModeMenu struct {
	menu           contextMenu
	open           bool
	at             image.Point
	button         surface
	items          [2]surface
	dismiss, panel byte
	// columns chooses how many thumbnails a row of the grid has, under
	// the ways, in a section of thumbnails.
	columns *slider.Slider
}

// gridSection tells whether the section shown is a grid of thumbnails.
func (p *chatInfo) gridSection() bool {
	switch p.section {
	case model.SharedPhotos, model.SharedVideos, model.SharedGIFs, model.SharedStories:
		return !p.themePage
	}
	return false
}

// gridColumns is how many thumbnails a row of the grid has: as chosen, 4
// unless it was.
func (p *chatInfo) gridColumns() int {
	if p.columns == nil {
		return preferences.SharedColumnsDefault
	}
	return min(max(p.columns(), preferences.SharedColumnsMin), preferences.SharedColumnsMax)
}

// columnsFooter is the height of the slider of the thumbnails a row has,
// with its title, under the menu's items.
const columnsFooter = 84

// layoutColumns draws the slider of how many thumbnails a row of the grid
// has, with its title and the number chosen.
func (p *chatInfo) layoutColumns(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	m := &p.mode
	n := p.gridColumns()
	if m.columns == nil {
		var options []int
		for i := preferences.SharedColumnsMin; i <= preferences.SharedColumnsMax; i++ {
			options = append(options, i)
		}
		m.columns = slider.StandardSlider(options, n, func(v int) {
			if p.setColumns != nil {
				if err := p.setColumns(v); err != nil {
					p.modal.Toast(mediaErrorText(err))
				}
				p.invalidate()
			}
		})
	}
	if m.columns.GetValue() != n {
		m.columns.SetValue(n)
	}
	sc := scheme(gtx)
	return layout.Inset{Top: 8, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("shared.columns")+": "+strconv.Itoa(n), token.TypestyleBodyMedium, sc.Surface.OnColor, 1)
			}),
			vspace(4),
			layout.Rigid(m.columns.Layout),
		)
	})
}

// cursorSelection tells whether a drag selects after the cursor.
func (p *chatInfo) cursorSelection() bool {
	return p.selectionMode != nil && p.selectionMode() == preferences.SharedSelectionCursor
}

// modeMenuUpdate opens the menu of how a drag selects, and takes what was
// chosen in it; call it before the dialog is laid out.
func (p *chatInfo) modeMenuUpdate(gtx layout.Context) {
	m := &p.mode
	if m.button.Clicked(gtx) && p.selectable() {
		m.open, m.at, m.menu = true, p.sharedMenu.pressAt, contextMenu{}
	}
	if !m.open {
		return
	}
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press}); !ok {
			break
		}
		m.open = false
	}
	for i, mode := range []string{"", preferences.SharedSelectionCursor} {
		if m.open && m.items[i].Clicked(gtx) {
			m.open = false
			if p.setSelectionMode != nil {
				if err := p.setSelectionMode(mode); err != nil {
					p.modal.Toast(mediaErrorText(err))
				}
			}
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	if !p.selectable() {
		m.open = false
	}
}

// layoutModeMenu draws the menu of how a drag selects, the way chosen in
// the primary color.
func (p *chatInfo) layoutModeMenu(gtx layout.Context, l localization.Catalog) {
	m := &p.mode
	cursor := p.cursorSelection()
	// In a grid, how many thumbnails its rows have, under the ways.
	var footer layout.Widget
	if p.gridSection() && p.setColumns != nil {
		footer = func(gtx layout.Context) layout.Dimensions { return p.layoutColumns(gtx, l) }
	}
	p.layoutInfoMenu(gtx, &m.menu, m.open, m.at, &m.dismiss, &m.panel, []infoMenuItem{
		{item: &m.items[0], icon: iconSelectInOrder, text: l.T("shared.select_range"), chosen: !cursor},
		{item: &m.items[1], icon: iconSelectAfterCursor, text: l.T("shared.select_cursor"), chosen: cursor},
	}, footer, gtx.Dp(columnsFooter))
}

// The icons of the ways a drag selects.
var (
	iconSelectInOrder     = wdk.RequireIconWidget(icons.ContentLowPriority)
	iconSelectAfterCursor = wdk.RequireIconWidget(icons.HardwareMouse)
)

// segmentHits tells whether the segment from a to b crosses r, its end
// edges excluded: the slabs of r's sides, as Liang and Barsky clip it.
func segmentHits(a, b f32.Point, r image.Rectangle) bool {
	t0, t1 := float32(0), float32(1)
	for _, axis := range [2]struct{ from, delta, lo, hi float32 }{
		{a.X, b.X - a.X, float32(r.Min.X), float32(r.Max.X)},
		{a.Y, b.Y - a.Y, float32(r.Min.Y), float32(r.Max.Y)},
	} {
		if axis.delta == 0 {
			if axis.from < axis.lo || axis.from >= axis.hi {
				return false
			}
			continue
		}
		u0, u1 := (axis.lo-axis.from)/axis.delta, (axis.hi-axis.from)/axis.delta
		if u0 > u1 {
			u0, u1 = u1, u0
		}
		t0, t1 = max(t0, u0), min(t1, u1)
		if t0 > t1 {
			return false
		}
	}
	return true
}

// selectCrossed selects, or unselects, the files the pointer crossed
// from a to b.
func (p *chatInfo) selectCrossed(a, b f32.Point) {
	s := &p.renderer.selection
	if s.selected == nil {
		s.selected = map[model.MessageID]bool{}
	}
	for _, place := range p.places {
		if place.index >= len(p.messages) || !segmentHits(a, b, place.rect) {
			continue
		}
		id := p.messages[place.index].Key.MessageID
		if p.drag.selecting {
			s.selected[id] = true
		} else {
			delete(s.selected, id)
		}
	}
}

// dragTo goes on with the drag to pos: the range to the file there, or the
// files crossed since the event before.
func (p *chatInfo) dragTo(pos f32.Point) {
	d := &p.drag
	if d.cursor {
		p.selectCrossed(d.last, pos)
		d.last = pos
		return
	}
	p.rangeSelect(p.fileAt(pos))
}

// sharedPlace is where the file of index lies in the section's list in
// the last frame, in the list's coordinates.
type sharedPlace struct {
	index int
	rect  image.Rectangle
}

// sharedRow is a row of the list drawn in the frame: its height, and its
// files, a row of a grid's several, each from x0 to x1.
type sharedRow struct {
	height int
	tiles  []sharedTile
}

type sharedTile struct{ index, x0, x1 int }

// placeRow notes row i of the list as drawn, height high, with its tiles.
func (p *chatInfo) placeRow(i, height int, tiles ...sharedTile) {
	if p.drawnRows == nil {
		p.drawnRows = map[int]sharedRow{}
	}
	p.drawnRows[i] = sharedRow{height: height, tiles: append(p.drawnRows[i].tiles[:0], tiles...)}
}

// placeFiles works out where the files lie from the rows drawn and the
// list's position, width wide, and forgets the rows.
func (p *chatInfo) placeFiles(width, height int) {
	p.places = p.places[:0]
	y := -p.list.Position.Offset
	for i := p.list.Position.First; y < height; i++ {
		row, ok := p.drawnRows[i]
		if !ok {
			break
		}
		for _, t := range row.tiles {
			x1 := t.x1
			if x1 <= t.x0 {
				x1 = width
			}
			p.places = append(p.places, sharedPlace{index: t.index, rect: image.Rect(t.x0, y, x1, y+row.height)})
		}
		y += row.height
	}
	clear(p.drawnRows)
}

// fileAt is the index of the file at pos in the list, or nearest it, for
// a pointer between files, over the end of the list or out of it; -1
// for none drawn.
func (p *chatInfo) fileAt(pos f32.Point) int {
	pt := pos.Round()
	best, bestDist := -1, 0
	for _, place := range p.places {
		r := place.rect
		dx := max(r.Min.X-pt.X, 0, pt.X-(r.Max.X-1))
		dy := max(r.Min.Y-pt.Y, 0, pt.Y-(r.Max.Y-1))
		// Rows before columns: past a row's end is its last file, not the
		// first of the next row.
		d := dy*dy*4 + dx*dx
		if best < 0 || d < bestDist {
			best, bestDist = place.index, d
		}
	}
	return best
}

// rangeSelect selects, or unselects, the files from the anchor to end,
// over the selection the drag started from.
func (p *chatInfo) rangeSelect(end int) {
	d := &p.drag
	if end < 0 || d.anchor >= len(p.messages) {
		return
	}
	end = min(end, len(p.messages)-1)
	selected := make(map[model.MessageID]bool, len(d.before)+1)
	for id, v := range d.before {
		if v {
			selected[id] = true
		}
	}
	for i := min(d.anchor, end); i <= max(d.anchor, end); i++ {
		id := p.messages[i].Key.MessageID
		if d.selecting {
			selected[id] = true
		} else {
			delete(selected, id)
		}
	}
	p.renderer.selection.selected = selected
}

// startDrag starts selecting from the file d.anchor, as the pointer of e
// drags.
func (p *chatInfo) startDrag(gtx layout.Context, e pointer.Event) {
	d := &p.drag
	d.before = make(map[model.MessageID]bool, len(p.renderer.selection.selected))
	for id, v := range p.renderer.selection.selected {
		d.before[id] = v
	}
	d.selecting = !p.renderer.selection.selected[p.messages[d.anchor].Key.MessageID]
	d.dragging, d.pointer, d.position, d.scrollAt = true, e.PointerID, e.Position, gtx.Now
	// After the cursor, the way from where it was pressed counts too.
	d.cursor, d.last = p.cursorSelection(), d.origin
	p.rangeSelect(d.anchor)
	// The file under the pointer takes no click of it.
	gtx.Execute(pointer.GrabCmd{Tag: &d.tag, ID: e.PointerID})
	gtx.Execute(op.InvalidateCmd{})
}

// dragEvents takes the presses and drags over the list, whose files lie
// where the last frame placed them; height is the list's.
func (p *chatInfo) dragEvents(gtx layout.Context, height int) {
	d := &p.drag
	if !p.selectable() {
		*d = sharedDrag{}
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &d.tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			// With nothing selected, a press is the file's own.
			if d.dragging || !p.selecting() || e.Source == pointer.Mouse && e.Buttons != pointer.ButtonPrimary {
				continue
			}
			i := p.fileAt(e.Position)
			if i < 0 {
				continue
			}
			d.anchor, d.pointer, d.origin = i, e.PointerID, e.Position
			p.startDrag(gtx, e)
		case pointer.Drag, pointer.Release:
			if !d.dragging || e.PointerID != d.pointer {
				continue
			}
			d.position = e.Position
			p.dragTo(e.Position)
			if e.Kind == pointer.Release {
				d.dragging = false
			}
		case pointer.Cancel:
			if d.dragging {
				p.renderer.selection.selected = d.before
				d.dragging = false
			}
		}
	}
	if !d.dragging {
		return
	}
	// Near the list's top or bottom, it scrolls on, and the range with it.
	edge := float32(gtx.Dp(28))
	speed := float32(0)
	if d.position.Y < edge {
		speed = -1
	} else if d.position.Y > float32(height)-edge {
		speed = 1
	}
	if speed != 0 {
		dt := min(float32(gtx.Now.Sub(d.scrollAt).Seconds()), .05)
		p.list.Position.Offset += int(speed * float32(gtx.Dp(420)) * dt)
		p.list.Position.BeforeEnd = true
		// The files scrolled under the pointer, which the places of the
		// last frame have where they were.
		if d.cursor {
			p.selectCrossed(d.position, d.position)
		} else {
			p.rangeSelect(p.fileAt(d.position))
		}
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
	}
	d.scrollAt = gtx.Now
}

// dragArea takes the presses and drags over the list, size big; laid out
// over the files, it lets their input through.
func (p *chatInfo) dragArea(gtx layout.Context, size image.Point) {
	if !p.selectable() {
		return
	}
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &p.drag.tag)
}
