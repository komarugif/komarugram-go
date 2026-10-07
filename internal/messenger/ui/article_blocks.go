// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"math"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/model"
)

// measure is how big leaf is laid out in max, drawing nothing and leaving
// no fragment behind.
func (a *articleDraw) measure(gtx layout.Context, leaf int, max image.Point) image.Point {
	if leaf < 0 {
		return image.Point{}
	}
	start := len(a.r.text.fragments)
	macro := op.Record(gtx.Ops)
	gtx.Constraints = layout.Constraints{Max: max}
	dims := a.p.textFlow(gtx, a.r, &a.doc.leaves[leaf], image.Point{}, a.animate)
	macro.Stop()
	// A word wider than max runs past it, and the size does not say so:
	// the lines of text do.
	size := dims.Size
	for _, f := range a.r.text.fragments[start:] {
		for _, c := range f.Clusters {
			if c.Bounds.Max.X > size.X {
				size.X = c.Bounds.Max.X
			}
		}
	}
	a.r.text.fragments = a.r.text.fragments[:start]
	return size
}

// shiftFragments moves the fragments of text from the first on by d, for
// text laid out before its place was known.
func shiftFragments(text *textInteraction, first int, d image.Point) {
	for i := first; i < len(text.fragments); i++ {
		f := &text.fragments[i]
		f.Bounds = f.Bounds.Add(d)
		for j := range f.Clusters {
			f.Clusters[j].Bounds = f.Clusters[j].Bounds.Add(d)
		}
	}
}

// table draws a table: its title over a grid of cells whose columns are as
// wide as their text, shrunk to fit, its header tinted and its borders, as
// Telegram Desktop's messageMarkdownTable does. Telegram Desktop scrolls a
// table too wide sideways; this one wraps its cells' text instead.
func (a *articleDraw) table(gtx layout.Context, b *articleBlock, origin image.Point) int {
	sc := scheme(gtx)
	width := gtx.Constraints.Max.X
	y := a.below(gtx, b.title, origin, 0, 0, 0)
	if y > 0 {
		y += gtx.Dp(6)
	}
	places := tablePlaces(b.rows)
	padX, padY := gtx.Dp(8), gtx.Dp(6)
	ty := wdk.GetMaterialTheme(gtx).Typescale[token.TypestyleBodyLarge]
	line := gtx.Sp(ty.LineHeight)
	columns := a.columnWidths(gtx, b, places, width, padX)
	left := make([]int, len(columns)+1)
	for i, w := range columns {
		left[i+1] = left[i] + w
	}
	tableWidth := left[len(columns)]
	// A table wider than the article scrolls sideways under its view.
	scroll := a.r.articleState.tableScroll(b.id)
	shift := scroll.update(gtx, max(0, tableWidth-width), width)
	// Each cell is laid out where it starts, then moved to its row once
	// the heights of the rows are known.
	type laid struct {
		place          tablePlace
		cell           articleCell
		call           op.CallOp
		size           image.Point
		first, through int
	}
	var cells []laid
	heights := make([]int, len(b.rows))
	for i := range heights {
		heights[i] = line + 2*padY
	}
	for i, row := range places {
		for j, p := range row {
			cell := b.rows[i].cells[j]
			w := left[min(p.column+p.colspan, len(columns))] - left[min(p.column, len(columns))]
			inner := gtx
			inner.Constraints = layout.Constraints{Max: image.Pt(max(1, w-2*padX), gtx.Constraints.Max.Y)}
			first := len(a.r.text.fragments)
			macro := op.Record(gtx.Ops)
			dims := a.flow(inner, cell.leaf, origin.Add(image.Pt(left[min(p.column, len(columns))]+padX, 0)))
			call := macro.Stop()
			cells = append(cells, laid{place: p, cell: cell, call: call, size: dims.Size, first: first, through: len(a.r.text.fragments)})
			if p.rowspan == 1 {
				heights[i] = max(heights[i], dims.Size.Y+2*padY)
			}
		}
	}
	// A cell spanning rows that they are too low for makes the last one
	// higher.
	for _, c := range cells {
		if c.place.rowspan > 1 {
			last := c.place.row + c.place.rowspan - 1
			spanned := 0
			for r := c.place.row; r <= last; r++ {
				spanned += heights[r]
			}
			if need := c.size.Y + 2*padY; need > spanned {
				heights[last] += need - spanned
			}
		}
	}
	top := make([]int, len(heights)+1)
	for i, h := range heights {
		top[i+1] = top[i] + h
	}
	size := image.Pt(tableWidth, top[len(heights)])
	radius := gtx.Dp(6)
	view := image.Pt(min(tableWidth, width), size.Y)
	offset(gtx, image.Pt(0, y), func(gtx layout.Context) layout.Dimensions {
		defer clip.Rect{Max: view}.Push(gtx.Ops).Pop()
		if tableWidth > width {
			// Its view takes the sideways scrolling; what is pressed in it
			// reaches the text under it.
			pass := pointer.PassOp{}.Push(gtx.Ops)
			scroll.scroll.Add(gtx.Ops)
			pass.Pop()
		}
		defer op.Offset(image.Pt(-shift, 0)).Push(gtx.Ops).Pop()
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		for _, c := range cells {
			col := min(c.place.column, len(columns))
			end := min(c.place.column+c.place.colspan, len(columns))
			rect := image.Rect(left[col], top[c.place.row], left[end], top[c.place.row+c.place.rowspan])
			switch {
			case c.cell.header:
				offset(gtx, rect.Min, func(gtx layout.Context) layout.Dimensions {
					fillRect(gtx, sc.Primary.Color.SetOpacity(.12), rect.Size())
					return layout.Dimensions{}
				})
			case b.striped && c.place.row%2 == 1:
				offset(gtx, rect.Min, func(gtx layout.Context) layout.Dimensions {
					fillRect(gtx, sc.SurfaceVariant.Color.SetOpacity(.45), rect.Size())
					return layout.Dimensions{}
				})
			}
			dy := padY
			switch c.cell.valign {
			case "middle":
				dy = (rect.Dy() - c.size.Y) / 2
			case "bottom":
				dy = rect.Dy() - padY - c.size.Y
			}
			at := image.Pt(rect.Min.X+padX, rect.Min.Y+dy)
			offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
				c.call.Add(gtx.Ops)
				return layout.Dimensions{}
			})
			// The cell was laid out at the top of the table.
			for i := c.first; i < c.through; i++ {
				f := &a.r.text.fragments[i]
				d := image.Pt(-shift, y+at.Y)
				f.Bounds = f.Bounds.Add(d)
				for j := range f.Clusters {
					f.Clusters[j].Bounds = f.Clusters[j].Bounds.Add(d)
				}
			}
		}
		if b.bordered {
			border := max(gtx.Dp(1), 1)
			for _, c := range cells {
				col := min(c.place.column, len(columns))
				end := min(c.place.column+c.place.colspan, len(columns))
				bottom := c.place.row + c.place.rowspan
				if end < len(columns) {
					offset(gtx, image.Pt(left[end], top[c.place.row]), func(gtx layout.Context) layout.Dimensions {
						fillRect(gtx, sc.OutlineVariant, image.Pt(border, top[bottom]-top[c.place.row]))
						return layout.Dimensions{}
					})
				}
				if bottom < len(heights) {
					offset(gtx, image.Pt(left[col], top[bottom]), func(gtx layout.Context) layout.Dimensions {
						fillRect(gtx, sc.OutlineVariant, image.Pt(left[end]-left[col], border))
						return layout.Dimensions{}
					})
				}
			}
			stroke(gtx, image.Rectangle{Max: size}, radius, border*2, sc.OutlineVariant.AsNRGBA())
		}
		return layout.Dimensions{Size: size}
	})
	y += size.Y
	if tableWidth > width {
		y += scroll.bar(gtx, y, tableWidth, width)
	}
	return y
}

// tableScroll is how far a table wider than its article is scrolled
// sideways, by the wheel, a touchpad or its scrollbar.
type tableScroll struct {
	x      int
	scroll gesture.Scroll
	drag   gesture.Drag
	// from and start are where a drag of the thumb began, and x then.
	from  float32
	start int
}

func (s *articleState) tableScroll(id int) *tableScroll {
	if s.scrolls == nil {
		s.scrolls = map[int]*tableScroll{}
	}
	t := s.scrolls[id]
	if t == nil {
		t = new(tableScroll)
		s.scrolls[id] = t
	}
	return t
}

// update applies the scrolling and the drags of the thumb since the last
// frame, and returns how far the table is scrolled: up to most, for a view
// width wide.
func (s *tableScroll) update(gtx layout.Context, most, width int) int {
	if most <= 0 {
		s.x = 0
		return 0
	}
	s.x += s.scroll.Update(gtx.Metric, gtx.Source, gtx.Now, gesture.Horizontal, pointer.ScrollRange{Min: -s.x, Max: most - s.x}, pointer.ScrollRange{})
	for {
		e, ok := s.drag.Update(gtx.Metric, gtx.Source, gesture.Horizontal)
		if !ok {
			break
		}
		switch e.Kind {
		case pointer.Press:
			s.from, s.start = e.Position.X, s.x
		case pointer.Drag:
			// The thumb moves over the track as the table under the view.
			track := width - s.thumb(most+width, width)
			if track > 0 {
				s.x = s.start + int((e.Position.X-s.from)*float32(most)/float32(track))
			}
		}
	}
	s.x = min(max(s.x, 0), most)
	return s.x
}

// thumb is how wide the thumb of a view width wide over a table total wide
// is: as the view of the table, at least 20 dp.
func (s *tableScroll) thumb(total, width int) int {
	return max(width*width/max(total, 1), min(width, 20))
}

// bar draws the scrollbar under a table total wide in a view width wide,
// at y, as Telegram Desktop's (messageMarkdownTable: 3 px under it, 10 px
// high), and returns how high it is with the space over it.
func (s *tableScroll) bar(gtx layout.Context, y, total, width int) int {
	skip, h := gtx.Dp(3), gtx.Dp(10)
	thumb := max(s.thumb(total, width), gtx.Dp(20))
	x := 0
	if most := total - width; most > 0 {
		x = s.x * (width - thumb) / most
	}
	sc := scheme(gtx)
	offset(gtx, image.Pt(0, y+skip), func(gtx layout.Context) layout.Dimensions {
		track := image.Rect(0, h/2-h/6, width, h/2+h/6)
		paint.FillShape(gtx.Ops, sc.OutlineVariant.SetOpacity(.5).AsNRGBA(), clip.UniformRRect(track, track.Dy()/2).Op(gtx.Ops))
		rect := image.Rect(x, 0, x+thumb, h)
		col := sc.SurfaceVariant.OnColor.SetOpacity(.45)
		if s.drag.Dragging() {
			col = sc.SurfaceVariant.OnColor.SetOpacity(.7)
		}
		paint.FillShape(gtx.Ops, col.AsNRGBA(), clip.UniformRRect(rect, h/2).Op(gtx.Ops))
		area := clip.Rect(rect).Push(gtx.Ops)
		pointer.CursorPointer.Add(gtx.Ops)
		s.drag.Add(gtx.Ops)
		area.Pop()
		return layout.Dimensions{}
	})
	return skip + h
}

// columnWidths shares width between a table's columns, as Telegram
// Desktop's ComputeTableColumnWidths does: each as wide as its widest cell
// when they all fit, and spread to fill it; otherwise each at least as wide
// as its longest word, and as its text up to a least width, the rest shared
// as their text asks for it. When even that does not fit, the columns are
// at those widths, wider than width together, and the table scrolls.
func (a *articleDraw) columnWidths(gtx layout.Context, b *articleBlock, places [][]tablePlace, width, padX int) []int {
	n := b.columns
	natural := make([]int, n)
	least := make([]int, n)
	// tdesktop's minColumnWidth, 96 px at its 13 px text.
	floor := gtx.Dp(118)
	for i, row := range places {
		for j, p := range row {
			if p.colspan != 1 || p.column >= n {
				continue
			}
			leaf := b.rows[i].cells[j].leaf
			w := a.measure(gtx, leaf, image.Pt(width*8, gtx.Constraints.Max.Y)).X + 2*padX
			// Text wraps at words only: laid out 1 px wide, it is as wide
			// as its longest word.
			word := a.measure(gtx, leaf, image.Pt(1, gtx.Constraints.Max.Y)).X + 2*padX
			natural[p.column] = max(natural[p.column], w)
			least[p.column] = max(least[p.column], word, min(w, floor))
		}
	}
	total, base := 0, 0
	for i := range natural {
		if natural[i] == 0 {
			natural[i] = 2*padX + gtx.Dp(16)
			least[i] = natural[i]
		}
		total += natural[i]
		base += least[i]
	}
	out := make([]int, n)
	switch {
	case total <= width:
		for i := range out {
			out[i] = natural[i] * width / total
		}
	case base >= width:
		copy(out, least)
		return out
	default:
		for i := range out {
			out[i] = least[i] + (width-base)*(natural[i]-least[i])/max(1, total-base)
		}
	}
	sum := 0
	for _, w := range out[:n-1] {
		sum += w
	}
	out[n-1] = width - sum
	return out
}

// details draws details: a header with their summary, which opens and
// closes them, over their blocks when they are open. As in Telegram for
// Android, the blocks unfold: their height grows from the header's, they
// fade in as it does, and the arrow turns; closing, the same backwards.
func (a *articleDraw) details(gtx layout.Context, b *articleBlock, origin image.Point) int {
	sc := scheme(gtx)
	s := &a.r.articleState
	toggle := s.toggle(b.id)
	if toggle.Clicked(gtx) {
		if s.toggled == nil {
			s.toggled = map[int]bool{}
		}
		s.toggled[b.id] = !s.toggled[b.id]
		gtx.Execute(op.InvalidateCmd{})
	}
	open := b.open != s.toggled[b.id]
	if s.opening == nil {
		s.opening = map[int]*heightTransition{}
	}
	unfold := s.opening[b.id]
	if unfold == nil {
		unfold = &heightTransition{}
		s.opening[b.id] = unfold
	}
	animate := a.animate && !s.instant[b.id]
	delete(s.instant, b.id)
	width := gtx.Constraints.Max.X
	pad := image.Pt(gtx.Dp(10), gtx.Dp(8))
	icon := gtx.Dp(24)
	inner := gtx
	inner.Constraints.Max.X = max(1, width-2*pad.X-icon-gtx.Dp(8))
	macro := op.Record(gtx.Ops)
	title := offset(inner, pad, func(gtx layout.Context) layout.Dimensions { return a.flow(gtx, b.title, origin.Add(pad)) })
	header := max(title.Size.Y, icon) + 2*pad.Y
	// The body is laid out while it shows, closing too, and clipped to
	// the height it has come to.
	shown := unfold.value
	if !unfold.initialized || unfold.width != width {
		shown = 0
	}
	var body op.CallOp
	full := 0
	if open || shown > 0 {
		pos := image.Pt(pad.X, header)
		inner := gtx
		inner.Constraints.Max.X = max(1, width-2*pad.X)
		bodyMacro := op.Record(gtx.Ops)
		full = offset(inner, pos, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: a.stack(gtx, b.children, origin.Add(pos))}
		}).Size.Y + pad.Y
		body = bodyMacro.Stop()
	}
	target := 0
	if open {
		target = full
	}
	shown = min(unfold.Value(gtx, target, animate), full)
	if shown > 0 {
		area := clip.Rect{Max: image.Pt(width, header+shown)}.Push(gtx.Ops)
		if shown < full {
			fade := paint.PushOpacity(gtx.Ops, float32(shown)/float32(full))
			body.Add(gtx.Ops)
			fade.Pop()
		} else {
			body.Add(gtx.Ops)
		}
		area.Pop()
	}
	// The arrow turns from down to up as the body opens.
	turn := float32(0)
	if full > 0 {
		turn = float32(shown) / float32(full)
	} else if open {
		turn = 1
	}
	offset(gtx, image.Pt(width-pad.X-icon, (header-icon)/2), func(gtx layout.Context) layout.Dimensions {
		center := f32.Pt(float32(icon)/2, float32(icon)/2)
		defer op.Affine(f32.AffineId().Rotate(center, turn*math.Pi)).Push(gtx.Ops).Pop()
		return exact(gtx, image.Pt(icon, icon), func(gtx layout.Context) layout.Dimensions { return iconExpandMore(gtx, sc.SurfaceVariant.OnColor) })
	})
	// The header takes the click over its text.
	toggle.Layout(gtx, image.Pt(width, header), surfaceStyle{radius: gtx.Dp(8), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: a.l.T("text.expand_quote")}, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(width, header)}
	})
	if shown < full {
		gtx.Execute(op.InvalidateCmd{})
	}
	h := header + shown
	call := macro.Stop()
	size := image.Pt(width, h)
	radius := gtx.Dp(8)
	offset(gtx, image.Point{}, func(gtx layout.Context) layout.Dimensions {
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		fillRect(gtx, sc.SurfaceVariant.Color.SetOpacity(.35), image.Pt(width, header))
		return layout.Dimensions{}
	})
	call.Add(gtx.Ops)
	stroke(gtx, image.Rectangle{Max: size}, radius, max(gtx.Dp(1), 1), sc.OutlineVariant.AsNRGBA())
	return h
}

// mediaMessage is media of the article of message m as a message of its
// own, for the media that draw and open messages' media.
func mediaMessage(m model.Message, media model.RichMedia) model.Message {
	return model.Message{Key: m.Key, Kind: media.Kind, Media: media.Media, ContentRevision: m.ContentRevision, NoForwards: m.NoForwards}
}

// mediaRow is the row of a photo or video of the article: it opens alone,
// not among the chat's photos.
func (a *articleDraw) mediaRow(id string) *messageRow {
	s := &a.r.articleState
	if s.media == nil {
		s.media = map[string]*messageRow{}
	}
	r := s.media[id]
	if r == nil {
		r = &messageRow{alone: true}
		s.media[id] = r
	}
	return r
}

// visual reports whether media is drawn as a picture: a photo, a video or
// a GIF; audio and files are drawn as cards.
func visual(media model.RichMedia) bool {
	switch media.Kind {
	case model.MessagePhoto, model.MessageVideo, model.MessageGIF:
		return true
	}
	return false
}

// mediaBlock draws a media block: one item, a collage of them or a
// slideshow, and its caption under it.
func (a *articleDraw) mediaBlock(gtx layout.Context, b *articleBlock, origin image.Point) int {
	width := gtx.Constraints.Max.X
	if a.lazy {
		if h := mediaBlockHeight(gtx.Metric, b, width); origin.Y+h < a.lo || origin.Y > a.hi {
			// Media far from the view are not loaded; their place is kept.
			fillRounded(gtx, scheme(gtx).SurfaceVariant.Color.SetOpacity(.55), image.Pt(width, h), gtx.Dp(6))
			return a.below(gtx, b.caption, origin, 0, h, 6)
		}
	}
	y := 0
	switch {
	case len(b.media) == 1:
		y = a.mediaItem(gtx, b.media[0], width, 0)
	case b.slideshow:
		y = a.slideshow(gtx, b, width)
	case len(b.media) > 1:
		y = a.collage(gtx, b.media, width)
	}
	return a.below(gtx, b.caption, origin, 0, y, 6)
}

// mediaItem draws one item width wide, height high, or as high as its
// shape asks for, bounded, for 0.
func (a *articleDraw) mediaItem(gtx layout.Context, media model.RichMedia, width, height int) int {
	if !visual(media) {
		return a.fileCard(gtx, media, width)
	}
	crop := height > 0
	if height == 0 {
		height, crop = mediaHeight(gtx.Metric, media, width)
	}
	size := image.Pt(width, height)
	msg := mediaMessage(a.m, media)
	return offset(gtx, image.Point{}, func(gtx layout.Context) layout.Dimensions {
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(6)).Push(gtx.Ops).Pop()
		gtx.Constraints = layout.Exact(size)
		return a.p.mediaTile(gtx, a.mediaRow(media.Media.ID), msg, size, crop, a.l, a.animate)
	}).Size.Y
}

// mediaHeight is how high a photo or a video is drawn width wide: as its
// shape asks for, bounded, and then cropped to it.
func mediaHeight(m unit.Metric, media model.RichMedia, width int) (height int, crop bool) {
	height = width * 9 / 16
	if media.Media.Width > 0 && media.Media.Height > 0 {
		height = width * media.Media.Height / media.Media.Width
	}
	if bounded := min(max(height, m.Dp(80)), m.Dp(480)); bounded != height {
		return bounded, true
	}
	return height, false
}

// pairRow is where a collage puts two photos or videos side by side, width
// wide: as high as fits them, the first w0 wide.
func pairRow(m unit.Metric, row [2]model.RichMedia, width int) (h, w0 int) {
	gap := m.Dp(2)
	ratio := func(m model.RichMedia) float32 {
		if m.Media.Width > 0 && m.Media.Height > 0 {
			return float32(m.Media.Width) / float32(m.Media.Height)
		}
		return 1
	}
	r0, r1 := ratio(row[0]), ratio(row[1])
	h = int(float32(width-gap) / (r0 + r1))
	h = min(max(h, m.Dp(60)), m.Dp(320))
	w0 = min(max(int(float32(h)*r0), m.Dp(40)), width-gap-m.Dp(40))
	return h, w0
}

// slideshowHeight is how high a slideshow of media is, width wide: as its
// highest item, so that it does not jump.
func slideshowHeight(m unit.Metric, media []model.RichMedia, width int) int {
	height := m.Dp(160)
	for _, item := range media {
		if visual(item) && item.Media.Width > 0 && item.Media.Height > 0 {
			height = max(height, min(width*item.Media.Height/item.Media.Width, m.Dp(480)))
		}
	}
	return height
}

// collage draws items two to a row, each row as high as fits them side
// by side, a last one alone across.
func (a *articleDraw) collage(gtx layout.Context, items []model.RichMedia, width int) int {
	gap := gtx.Dp(2)
	y := 0
	for i := 0; i < len(items); i += 2 {
		if i > 0 {
			y += gap
		}
		row := items[i:min(i+2, len(items))]
		if len(row) == 1 || !visual(row[0]) || !visual(row[1]) {
			for j, item := range row {
				if j > 0 {
					y += gap
				}
				pos := image.Pt(0, y)
				y += offset(gtx, pos, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: image.Pt(width, a.mediaItem(gtx, item, width, 0))}
				}).Size.Y
			}
			continue
		}
		h, w0 := pairRow(gtx.Metric, [2]model.RichMedia{row[0], row[1]}, width)
		pos := image.Pt(0, y)
		offset(gtx, pos, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(w0, a.mediaItem(gtx, row[0], w0, h))}
		})
		pos = image.Pt(w0+gap, y)
		offset(gtx, pos, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(width-w0-gap, a.mediaItem(gtx, row[1], width-w0-gap, h))}
		})
		y += h
	}
	return y
}

// slideshow draws one item of a slideshow at a time, with buttons to the
// one before and after, and dots that tell which it is.
func (a *articleDraw) slideshow(gtx layout.Context, b *articleBlock, width int) int {
	sc := scheme(gtx)
	s := &a.r.articleState
	if s.slides == nil {
		s.slides, s.arrows = map[int]int{}, map[int]*[2]surface{}
	}
	arrows := s.arrows[b.id]
	if arrows == nil {
		arrows = new([2]surface)
		s.arrows[b.id] = arrows
	}
	n := len(b.media)
	current := min(s.slides[b.id], n-1)
	if arrows[0].Clicked(gtx) && current > 0 {
		current--
	}
	if arrows[1].Clicked(gtx) && current < n-1 {
		current++
	}
	s.slides[b.id] = current
	height := slideshowHeight(gtx.Metric, b.media, width)
	a.mediaItem(gtx, b.media[current], width, height)
	button := gtx.Dp(32)
	for i, glyph := range []wdk.IconWidget{iconChevronLeft, iconChevron} {
		if i == 0 && current == 0 || i == 1 && current == n-1 {
			continue
		}
		x := gtx.Dp(8)
		if i == 1 {
			x = width - gtx.Dp(8) - button
		}
		offset(gtx, image.Pt(x, (height-button)/2), func(gtx layout.Context) layout.Dimensions {
			style := surfaceStyle{radius: button / 2, background: sc.Surface.Color.SetOpacity(.8), content: sc.Surface.OnColor, button: a.l.T("chat_search.older")}
			return arrows[i].Layout(gtx, image.Pt(button, button), style, func(gtx layout.Context) layout.Dimensions {
				px := gtx.Dp(22)
				return offset(gtx, image.Pt((button-px)/2, (button-px)/2), func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions { return glyph(gtx, sc.Surface.OnColor) })
				})
			})
		})
	}
	dot, gap := gtx.Dp(6), gtx.Dp(5)
	x := (width - n*dot - (n-1)*gap) / 2
	for i := range n {
		col := color.NRGBA{R: 255, G: 255, B: 255, A: 120}
		if i == current {
			col.A = 255
		}
		at := image.Pt(x+i*(dot+gap), height-gtx.Dp(10)-dot)
		paint.FillShape(gtx.Ops, col, clip.Ellipse(image.Rectangle{Min: at, Max: at.Add(image.Pt(dot, dot))}).Op(gtx.Ops))
	}
	return height
}

// fileCard draws audio or a file: its icon, title and what is under it,
// on a plate that opens it.
func (a *articleDraw) fileCard(gtx layout.Context, media model.RichMedia, width int) int {
	sc := scheme(gtx)
	s := &a.r.articleState
	if s.files == nil {
		s.files = map[string]*surface{}
	}
	card := s.files[media.Media.ID]
	if card == nil {
		card = new(surface)
		s.files[media.Media.ID] = card
	}
	msg := mediaMessage(a.m, media)
	if card.Clicked(gtx) {
		a.p.openAttachment(msg)
	}
	title, sub := media.Media.Title, media.Media.Performer
	if title == "" {
		title, sub = media.Media.FileName, ""
	}
	icon := iconAttachFile
	if media.Kind == model.MessageMusic || media.Kind == model.MessageVoice {
		icon = iconMusic
		if title == "" {
			title = a.l.T("rich.audio")
		}
	} else if title == "" {
		title = a.l.T("rich.file")
	}
	if sub == "" && media.Media.Size > 0 {
		sub = sizeText(a.l, media.Media.Size)
	}
	size := image.Pt(width, gtx.Dp(56))
	style := surfaceStyle{radius: gtx.Dp(8), background: sc.SurfaceVariant.Color.SetOpacity(.55), content: sc.Surface.OnColor, button: title}
	return card.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		d := gtx.Dp(40)
		offset(gtx, image.Pt(gtx.Dp(8), (size.Y-d)/2), func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse(image.Rectangle{Max: image.Pt(d, d)}).Op(gtx.Ops))
			px := gtx.Dp(22)
			return offset(gtx, image.Pt((d-px)/2, (d-px)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions { return icon(gtx, sc.Primary.OnColor) })
			})
		})
		x := gtx.Dp(8) + d + gtx.Dp(10)
		text := gtx
		text.Constraints = layout.Constraints{Max: image.Pt(max(1, size.X-x-gtx.Dp(8)), size.Y)}
		offset(text, image.Pt(x, gtx.Dp(9)), func(gtx layout.Context) layout.Dimensions {
			return label(gtx, title, token.TypestyleTitleSmall, sc.Surface.OnColor, 1)
		})
		if sub != "" {
			offset(text, image.Pt(x, gtx.Dp(30)), func(gtx layout.Context) layout.Dimensions {
				return label(gtx, sub, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
			})
		}
		return layout.Dimensions{Size: size}
	}).Size.Y
}

// buttonRow draws a row of buttons, as wide as their labels and aligned as
// the row says, or sharing it all; a click presses one as the same button
// under a message is pressed.
func (a *articleDraw) buttonRow(gtx layout.Context, b *articleBlock) int {
	sc := scheme(gtx)
	s := &a.r.articleState
	surfaces := s.surfaces(&s.buttons, b.id, len(b.buttons))
	width := gtx.Constraints.Max.X
	h, gap, pad := gtx.Dp(34), gtx.Dp(6), gtx.Dp(14)
	n := len(b.buttons)
	if n == 0 {
		return 0
	}
	widths := make([]int, n)
	total := 0
	for i, btn := range b.buttons {
		if b.align == "" {
			widths[i] = (width - gap*(n-1)) / n
		} else {
			macro := op.Record(gtx.Ops)
			dims := label(gtx, btn.Text.Text, token.TypestyleLabelLarge, sc.Surface.OnColor, 1)
			macro.Stop()
			widths[i] = dims.Size.X + 2*pad
		}
		total += widths[i]
	}
	total += gap * (n - 1)
	if total > width {
		for i := range widths {
			widths[i] = (width - gap*(n-1)) / n
		}
		total = width
	}
	x := 0
	switch b.align {
	case "center":
		x = (width - total) / 2
	case "right":
		x = width - total
	}
	for i, btn := range b.buttons {
		if surfaces[i].Clicked(gtx) {
			a.press(gtx, b.id, i, btn.Button)
		}
		bg, fg := sc.SecondaryContainer.Color, sc.SecondaryContainer.OnColor
		switch btn.Style {
		case "primary":
			bg, fg = sc.Primary.Color, sc.Primary.OnColor
		case "danger":
			fg = sc.Error.Color
		case "success":
			fg = token.NewMatColorFromHexRGB(0x2e7d32)
		case "link":
			bg, fg = sc.Primary.Color.SetOpacity(0), sc.Primary.Color
		}
		size := image.Pt(widths[i], h)
		text := btn.Text.Text
		offset(gtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions {
			style := surfaceStyle{radius: gtx.Dp(8), background: bg, content: fg, button: text}
			return surfaces[i].Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(size)
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Point{}
					gtx.Constraints.Max.X = max(1, size.X-2*gtx.Dp(6))
					return centeredLabel(gtx, text, token.TypestyleLabelLarge, fg, 1)
				})
			})
		})
		x += widths[i] + gap
	}
	return h
}

// press does what button i of block id is for, as the same button under a
// message does.
func (a *articleDraw) press(gtx layout.Context, id, i int, b model.MessageButton) {
	if a.r.streaming {
		// The buttons of a draft a bot streams work once it is a message,
		// as in Telegram Desktop.
		return
	}
	switch b.Kind {
	case "url":
		a.p.askLink(b.URL)
	case "webview", "simple_webview":
		a.p.pressWebView(gtx, a.p.chat, a.m, b)
	case "callback", "copy":
		// Rows of buttons in an article are told apart from the keyboard's.
		a.p.pressButton(gtx, a.m, articleButtonRows+id, i, b, a.l)
	}
}

// articleButtonRows is where the rows of an article's buttons count from,
// past the rows of a keyboard under a message.
const articleButtonRows = 1 << 20

// card draws what the article names rather than shows: an icon, a title
// and lines under it, on a plate that opens its link; related articles,
// one card each, under their title.
func (a *articleDraw) card(gtx layout.Context, b *articleBlock, origin image.Point) int {
	width := gtx.Constraints.Max.X
	y := a.below(gtx, b.title, origin, 0, 0, 0)
	cards := []articleCardData{b.card}
	if len(b.card.related) > 0 {
		cards = b.card.related
	}
	s := &a.r.articleState
	surfaces := s.surfaces(&s.cards, b.id, len(cards))
	for i, c := range cards {
		if y > 0 {
			y += gtx.Dp(6)
		}
		if surfaces[i].Clicked(gtx) && c.url != "" {
			a.p.askLink(c.url)
		}
		pos := image.Pt(0, y)
		y += offset(gtx, pos, func(gtx layout.Context) layout.Dimensions {
			return a.cardPlate(gtx, &surfaces[i], c, b.card.icon, width)
		}).Size.Y
	}
	return a.below(gtx, b.caption, origin, 0, y, 6)
}

func (a *articleDraw) cardPlate(gtx layout.Context, s *surface, c articleCardData, kind string, width int) layout.Dimensions {
	sc := scheme(gtx)
	pad := gtx.Dp(10)
	icon := iconInfo
	switch kind {
	case "link":
		icon = iconLink
	case "place":
		icon = iconPlace
	case "channel":
		icon = iconChannels
	}
	px := gtx.Dp(22)
	textX := pad + px + gtx.Dp(10)
	text := gtx
	text.Constraints = layout.Constraints{Max: image.Pt(max(1, width-textX-pad), gtx.Constraints.Max.Y)}
	macro := op.Record(gtx.Ops)
	y := pad
	if c.title != "" {
		y += offset(text, image.Pt(textX, y), func(gtx layout.Context) layout.Dimensions {
			return label(gtx, c.title, token.TypestyleTitleSmall, sc.Surface.OnColor, 2)
		}).Size.Y
	}
	for _, line := range c.lines {
		y += gtx.Dp(2)
		y += offset(text, image.Pt(textX, y), func(gtx layout.Context) layout.Dimensions {
			return label(gtx, line, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 2)
		}).Size.Y
	}
	offset(gtx, image.Pt(pad, pad), func(gtx layout.Context) layout.Dimensions {
		return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions { return icon(gtx, sc.Primary.Color) })
	})
	call := macro.Stop()
	size := image.Pt(width, max(y, pad+px)+pad)
	style := surfaceStyle{radius: gtx.Dp(8), background: sc.SurfaceVariant.Color.SetOpacity(.55), content: sc.Surface.OnColor, button: c.title}
	if c.url == "" {
		fillRounded(gtx, style.background, size, style.radius)
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}
