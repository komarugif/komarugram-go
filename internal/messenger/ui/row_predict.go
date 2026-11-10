// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"komarugram/internal/messenger/model"

	"gioui.org/unit"
)

// A row's profile tells how high the row is at a width it was not drawn
// at: predict lays its bubble out again from what the profile keeps, as
// bubbleBody does, with what bubbleBody decides by: captionFitWidth,
// replyLineCount, reactionFit, mediaBox and timeFits. The rest of the row,
// its insets, a date over it, is as high at any width.

// partProfile is a part of a bubble (bubblePart) as the layout the profile
// was taken from laid it out: its kind and size, and, for a picture sized
// by mediaBox, its kind and size before it was fitted; for a tiled album,
// the sizes of its pictures (albumTiles); for a caption of a part of an
// album, its text's profile. wide is set for a widget as wide as it could
// be there, as wide as it can be at any width.
type partProfile struct {
	kind   bubblePartKind
	edge   bool
	size   image.Point
	fit    bool
	media  model.MessageKind
	mw, mh int
	album  []image.Point
	text   *textProfile
	wide   bool
}

// replyProfile is the quote of a message replied to: how wide its name and
// one line of its text are of themselves, how high that line is, its
// padding and the narrowest it is, and its size on the lines it took. A
// quote of no text, as of a story, has lines 0, and its size at any width.
type replyProfile struct {
	named, line, lineHeight int
	pad, least              int
	lines                   int
	size                    image.Point
}

// partProfile is part as bubbleBody laid it out size large, in a bubble
// most wide.
func (p *chatPage) partProfile(r *messageRow, part bubblePart, size image.Point, most int) partProfile {
	pp := partProfile{kind: part.kind, edge: part.edge, size: size}
	switch part.kind {
	case partMedia:
		m := r.shown
		switch {
		case len(m.Attachments) > 1:
			if tiledAlbum(m.Attachments) {
				pp.album = append([]image.Point(nil), r.albumSizes...)
			}
		case m.Media != nil && boxedMedia(m.Kind):
			pp.fit, pp.media, pp.mw, pp.mh = true, m.Kind, m.Media.Width, m.Media.Height
		}
		pp.wide = !pp.fit && pp.album == nil && size.X >= most
	case partText:
		if part.row != nil {
			pp.text = part.row.capture.text
		}
	case partWidget:
		pp.wide = size.X >= most
	}
	return pp
}

// textSize is how large the text is laid out within width: its blocks, the
// widest of them across (code and a folded quote as wide as they may be, a
// quote as its text and padding), and how far its last line reaches where
// the text ends with a flow, its time may go after it.
func (t *textProfile) textSize(width int) (size image.Point, last int, lastOK bool, tallest int) {
	for i := range t.blocks {
		b := &t.blocks[i]
		if i > 0 {
			size.Y += t.gap
		}
		_, h, l, widest, tall := b.wrapAll(width)
		size.Y += b.chrome + h
		tallest = max(tallest, tall)
		w := min(widest, width-b.inset)
		switch b.kind {
		case blockCode, blockFolded:
			w = width
		case blockQuote:
			w = min(width, w+b.inset)
		}
		size.X = max(size.X, w)
		last, lastOK = l+b.left, b.kind == blockFlow
	}
	return size, last, lastOK, tallest
}

// predict is how high the row is, and how wide its bubble, laid out width
// wide (layoutWidth) in metric m. The width is at most bubbleWidth, not as
// wide exactly (exact false), where a line of one line of its own, as the
// name of whom a message was forwarded from, is cut short there: how wide
// it is then is of the glyphs it is cut at.
func (p *rowProfile) predict(m unit.Metric, width int) (height, bubbleWidth int, exact bool) {
	return p.predictShape(m, width, nil)
}

// predictShape is predict, putting in shape, when set, what the bubble
// shows where: its parts and its text's lines (skeleton_shape.go).
func (p *rowProfile) predictShape(m unit.Metric, width int, shape *[]shapeBar) (height, bubbleWidth int, exact bool) {
	b := &p.bubble
	exact = true
	// The bubble is as wide as the row lets it be, beside the avatar.
	room := min(width-m.Dp(rowInsetLeft)-m.Dp(rowInsetRight), m.Dp(bubbleMaxWidth)) - b.slot
	most := max(0, room-2*m.Dp(bubblePadSide))
	out := m.Dp(bubblePadSide) - m.Dp(mediaInset)
	parts := b.parts
	lift := 0
	for _, part := range parts {
		if part.kind == partSpace {
			continue
		}
		if part.edge {
			lift = m.Dp(bubblePadTop) - m.Dp(mediaInset)
		}
		break
	}
	last := -1
	for i, part := range parts {
		if part.kind != partSpace {
			last = i
		}
	}
	plate := image.Point{}
	if last >= 0 && parts[last].edge {
		// Laid out within the bubble, as the line of the time is: cut where
		// it is narrower.
		plate = image.Pt(min(b.plate.X, most), b.plate.Y)
	}
	foot := image.Pt(min(p.footer.X, most), p.footer.Y)
	media := false
	for _, part := range parts {
		media = media || part.kind == partMedia
	}
	sizes := make([]image.Point, len(parts))
	// textAt is the width each text part wrapped at.
	textAt := make([]int, len(parts))
	// lastTile is the last of an album's tiles, which its time goes on.
	var lastTile image.Point
	base, reply, reactions, text := 0, -1, -1, -1
	for i, part := range parts {
		switch {
		case part.kind == partSpace:
			sizes[i] = part.size
		case part.kind == partReply:
			reply = i
		case part.kind == partReactions:
			reactions = i
		case part.kind == partText:
			if part.text == nil {
				text = i
			}
			if !media && p.text != nil {
				sizes[i], _, _, _ = p.text.textSize(most)
				textAt[i] = most
				base = max(base, sizes[i].X)
			}
		case part.album != nil:
			maxX := most
			if part.edge {
				maxX += 2 * out
			}
			tiles, size := albumTiles(m, maxX, part.album, nil)
			sizes[i] = size
			if n := len(tiles); n > 0 {
				lastTile = tiles[n-1].Size()
			}
			if part.edge {
				base = max(base, size.X-2*out)
			} else {
				base = max(base, size.X)
			}
		case part.edge:
			sizes[i] = part.sized(m, most+2*out)
			base = max(base, sizes[i].X-2*out)
		default:
			sizes[i] = part.sized(m, most)
			base = max(base, sizes[i].X)
			if part.kind == partWidget && !part.wide && part.size.X > most {
				exact = false
			}
		}
	}
	lastRight, lastOK := 0, false
	if media {
		// The captions, the message's own or those of the album's parts,
		// all as narrow as the media let them be.
		captions := base
		for i, part := range parts {
			t := part.text
			if part.kind != partText {
				continue
			}
			if t == nil {
				t = p.text
			}
			if t == nil {
				continue
			}
			at := t.captionWidth(m, most, captions)
			size, right, ok, _ := t.textSize(at)
			sizes[i], textAt[i] = size, at
			base = max(base, size.X)
			if i == text {
				lastRight, lastOK = right, ok
			}
		}
	} else if text >= 0 && p.text != nil {
		_, lastRight, lastOK, _ = p.text.textSize(most)
	}
	timeOn := image.Point{}
	if last >= 0 {
		timeOn = sizes[last]
		if parts[last].album != nil {
			timeOn = lastTile
		}
	}
	onMedia := last >= 0 && parts[last].edge && plate.X > 0 && timeFits(timePlate{size: plate}, timeOn, m.Dp(8))
	if !onMedia {
		base = max(base, foot.X)
	}
	onText := lastOK && b.textLast && text >= 0
	if onText {
		if end := lastRight + m.Dp(footerGap) + foot.X; end <= most {
			base = max(base, end)
		} else {
			onText = false
		}
	}
	fit := reactionFit{widths: p.chips, gap: m.Dp(reactionGap), row: p.chipRow, base: base, most: most, foot: foot,
		footGap: m.Dp(footerGap), footSpace: m.Dp(footerSpace), worth: m.Dp(64)}
	if reply >= 0 {
		within := base
		if reactions >= 0 {
			_, within, _ = fit.at(fit.choose())
		}
		sizes[reply] = b.reply.sized(m, most, within)
		base = max(base, sizes[reply].X)
	}
	onChips, chipsRight := false, 0
	if reactions >= 0 {
		fit.base = base
		w := fit.choose()
		_, _, onChips = fit.at(w)
		rows, widest, end := reactionRows(fit.widths, fit.gap, w)
		sizes[reactions] = image.Pt(widest, rows*fit.row+(rows-1)*fit.gap)
		chipsRight = end
	}
	y, wide, mediaBottom := -lift, 0, 0
	for i, part := range parts {
		if i == last {
			mediaBottom = y + sizes[i].Y
		}
		top := y
		y += sizes[i].Y
		if part.kind == partSpace {
			continue
		}
		x := 0
		if part.edge {
			x = -out
		}
		wide = max(wide, sizes[i].X+2*x)
		if shape != nil {
			t := part.text
			if t == nil && part.kind == partText {
				t = p.text
			}
			at := image.Pt(m.Dp(bubblePadSide)+x, m.Dp(bubblePadTop)+top)
			*shape = appendShape(*shape, part.kind, image.Rectangle{Min: at, Max: at.Add(sizes[i])}, t, textAt[i])
		}
	}
	var body image.Point
	switch {
	case onMedia:
		body = image.Pt(wide, mediaBottom-(m.Dp(bubblePadBottom)-m.Dp(mediaInset)))
	case onText:
		body = image.Pt(max(wide, foot.X, lastRight+m.Dp(footerGap)+foot.X), y)
	case onChips:
		body = image.Pt(max(wide, foot.X, chipsRight+m.Dp(footerGap)+foot.X), y)
	default:
		body = image.Pt(max(wide, foot.X), y+m.Dp(footerSpace)+foot.Y)
	}
	bubble := body.Add(image.Pt(2*m.Dp(bubblePadSide), m.Dp(bubblePadTop)+m.Dp(bubblePadBottom)))
	// No wider than the row lets it be, as what it holds that is wider, a
	// quote at its narrowest, is cut.
	bubble.X = min(bubble.X, room)
	if b.bar > 0 {
		bubble.X = max(bubble.X, min(room, b.barNatural))
		bubble.Y += b.bar
	}
	return b.rest + max(bubble.Y, b.avatar), bubble.X, exact
}

// sized is how large the part is laid out within maxX.
func (pp *partProfile) sized(m unit.Metric, maxX int) image.Point {
	switch {
	case pp.fit:
		return mediaBox(m, pp.media, pp.mw, pp.mh, maxX)
	case pp.wide:
		return image.Pt(maxX, pp.size.Y)
	}
	return image.Pt(min(pp.size.X, maxX), pp.size.Y)
}

// sized is how large the quote is, wrapped within within in a bubble most
// wide.
func (q *replyProfile) sized(m unit.Metric, most, within int) image.Point {
	if q.lines == 0 {
		return image.Pt(min(q.size.X, most), q.size.Y)
	}
	// The name and the text are measured within the bubble, as replyLines
	// does, cut where it is narrower than they.
	room := max(0, most-q.pad)
	named, line := min(q.named, room), min(q.line, room)
	lines, w := replyLineCount(m, named, line, q.pad, q.least, within)
	h := q.size.Y + (lines-q.lines)*q.lineHeight
	return image.Pt(max(min(w, most), q.least), h)
}

// captionWidth is the width the text wraps at under media least wide in a
// bubble most wide, as captionWidth finds it.
func (t *textProfile) captionWidth(m unit.Metric, most, least int) int {
	for _, b := range t.blocks {
		if b.kind == blockCode || b.kind == blockFolded {
			return most
		}
	}
	tallest := 0
	return captionFitWidth(m, most, least, func() int { return tallest }, func(w int) int {
		size, _, _, tall := t.textSize(w)
		tallest = max(tallest, tall)
		return size.Y
	})
}
