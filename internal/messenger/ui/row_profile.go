// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/styledtext"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/go-text/typesetting/segmenter"
	"golang.org/x/image/math/fixed"
)

// A row's profile is what its layout at one width tells of its layout at
// any other: the widths of the pieces its text may break between, block by
// block, and the widths of what else wraps or moves with the width: its
// time, the quote of the message it replies to, its reactions. It is taken
// once, from the row drawn, never guessed, and kept with the heights
// (historycache's row_profiles), for the history to know how high its rows
// are at a width it has not drawn them at.

// piece is a run of a span of text between two places a line may break: as
// wide as body without the spaces it ends with, which are trail wide, the
// last of them last wide; a line ends after it where hard is set. At the
// end of a line its last space hangs past it, and the others take their
// room. reach is how far its glyphs reach over and under the baseline.
type piece struct {
	body, trail, last fixed.Int26_6
	hard              bool
	reach             styledtext.Extent
}

// spanProfile is a span of a flow: its pieces, how far the truncator that
// ends a line it goes on after reaches, how far its style moves it down (a
// subscript), and whether it is a box (a custom emoji, a tab).
type spanProfile struct {
	pieces    []piece
	truncator styledtext.Extent
	shift     int
	box       bool
}

// blockProfile is how a block of a text wraps: its spans, how much narrower
// than the text its lines are (a quote's or code's padding) and how far in
// they start, how much higher than its lines it is, and how many lines it
// shows, for a folded quote.
type blockProfile struct {
	kind     blockKind
	spans    []spanProfile
	inset    int
	left     int
	chrome   int
	maxLines int
}

// blockKind is what a block of a text is: a flow of it, a quote, a folded
// quote or code. Code and a folded quote are as wide as they may be.
type blockKind uint8

const (
	blockFlow blockKind = iota
	blockQuote
	blockFolded
	blockCode
)

// textProfile is how a message's text wraps: its blocks, gap apart.
type textProfile struct {
	blocks []blockProfile
	gap    int
}

// rowProfile is a row's profile: laid out width wide (the layout width,
// layoutWidth), it was height high, its bubble bubbleWidth wide.
type rowProfile struct {
	width, height, bubbleWidth int
	text                       *textProfile
	// footer is the size of the line of the time, onText whether it was on
	// the text's last line.
	footer image.Point
	onText bool
	// chips are the widths of the reactions' chips, chipRow how high a row
	// of them is.
	chips   []int
	chipRow int
	bubble  bubbleProfile
}

// bubbleProfile is the rest of what a bubble's layout depends on: its
// parts; the time on a plate over media that end it; whether its time may
// go after its text; the quote of the message replied to; the room an
// avatar takes beside it and how high it is; the comments bar under it,
// and how wide that is of itself; and how much higher the row is than it.
type bubbleProfile struct {
	parts           []partProfile
	plate           image.Point
	textLast        bool
	reply           replyProfile
	slot, avatar    int
	bar, barNatural int
	rest            int
}

// lineSet is the spans on a line, and tells how high it is, as styledtext
// sets them: the line as high as its highest span, or, with a box on it,
// all on the boxes' baseline.
type lineSet struct {
	text, textAscent int
	boxDown, boxUp   int
	boxed            bool
}

// add puts on the line the part of span that reaches as far as e.
func (l *lineSet) add(s *spanProfile, e styledtext.Extent) {
	if s.box {
		l.boxed = true
		l.boxUp = max(l.boxUp, e.Up)
		l.boxDown = max(l.boxDown, e.Down)
		return
	}
	l.text = max(l.text, e.Up+e.Down+s.shift)
	l.textAscent = max(l.textAscent, e.Up)
}

func (l *lineSet) height() int {
	if !l.boxed {
		return l.text
	}
	up := max(l.textAscent, l.boxUp)
	return max(up-l.textAscent+l.text, up+l.boxDown)
}

// wrap tells how the block's lines come out at width px: how many there
// are and how high they are, how far the last reaches and the widest. It
// wraps as styledtext lays the spans out, each from where the line is: a
// span breaks where its pieces allow, and goes to the next line where its
// first piece does not fit on this one. A piece wider than a line has a
// line of its own and goes past its end, as a word does: words are not cut
// (styledtext.WrapWords); code is cut into pieces of a grapheme.
func (b *blockProfile) wrap(width int) (lines, height, last, widest int) {
	lines, height, last, widest, _ = b.wrapAll(width)
	return lines, height, last, widest
}

// wrapAll is wrap, and how high the highest line is.
func (b *blockProfile) wrapAll(width int) (lines, height, last, widest, tallest int) {
	return b.wrapEach(width, nil)
}

// wrapEach is wrapAll, telling each, when set, how wide and how high each
// line the block shows is.
func (b *blockProfile) wrapEach(width int, each func(width, height int)) (lines, height, last, widest, tallest int) {
	w := max(width-b.inset, 1)
	// base is how wide the parts of spans that end on the line are: each is
	// laid out to a whole pixel, and the next starts there. run is how far
	// the span's part on it reaches, hang the spaces at its end.
	base := 0
	var run, hang fixed.Int26_6
	var line lineSet
	end := func() {
		// A word longer than a line goes past its end, drawn to it.
		last = min(base, w)
		widest = max(widest, last)
		lines++
		if b.maxLines == 0 || lines <= b.maxLines {
			height += line.height()
			if each != nil {
				each(last, line.height())
			}
		}
		tallest = max(tallest, line.height())
		base, run, hang, line = 0, 0, 0, lineSet{}
	}
	for i := range b.spans {
		span := &b.spans[i]
		// reach is how far the span's part on the line reaches, a truncator
		// after it where the span goes on on the next line.
		var reach styledtext.Extent
		on := false
		// put ends the span's part on the line: where the line ends after
		// it, its last space hangs past the end. Where the span ends, its
		// spaces are its own, before its word joiner.
		put := func(cut, ends bool) {
			if !on {
				return
			}
			if ends {
				run -= hang
			}
			base += run.Ceil()
			run, hang = 0, 0
			if cut {
				// Alone, where nothing else of the span is on the line, as
				// between paragraphs.
				reach = reach.Union(span.truncator)
			}
			line.add(span, reach)
			reach, on = styledtext.Extent{}, false
		}
		for k, p := range span.pieces {
			if (base > 0 || run > 0) && fixed.I(base)+run+p.body+p.trail-p.last > fixed.I(w) {
				put(true, true)
				end()
			}
			run += p.body + p.trail
			hang = p.last
			reach, on = reach.Union(p.reach), true
			if p.hard {
				put(k < len(span.pieces)-1, true)
				end()
			}
		}
		put(false, false)
	}
	// The last line, an empty one after a text that ends with the end of a
	// line: the word joiner styledtext ends a span with is on it.
	end()
	return lines, height, last, widest, tallest
}

// height is how high the block is at width px, with the lines it shows, how
// far its last line reaches and how wide its widest is.
func (b *blockProfile) height(width int) (height, last, widest int) {
	_, lines, last, widest := b.wrap(width)
	return b.chrome + lines, last + b.left, widest + b.inset
}

// height is how high the text is at width px, how far its last line
// reaches and how wide its widest is.
func (t *textProfile) height(width int) (height, last, widest int) {
	for i := range t.blocks {
		if i > 0 {
			height += t.gap
		}
		h, l, w := t.blocks[i].height(width)
		height += h
		last, widest = l, max(widest, w)
	}
	return height, last, widest
}

// profileScratch is what cutting spans into pieces works in, kept by the
// page for the next row.
type profileScratch struct {
	seg   segmenter.Segmenter
	runes []rune
	adv   []fixed.Int26_6
	ext   []styledtext.Extent
}

// wordJoiner ends each span as styledtext shapes it: no line breaks before
// it, so a span's last spaces are its own, not trailing a line.
const wordJoiner = '⁠'

// cutFlow cuts the spans of a flow laid out in gtx, whose runes moved the
// pen as far as adv tells and reached as far as ext (styledtext's Advances,
// Extents and Ends), into their pieces, at their words' ends, or, for code
// (graphemes), at every grapheme.
func (s *profileScratch) cutFlow(gtx layout.Context, spans []styledtext.SpanStyle, adv []fixed.Int26_6, ext []styledtext.Extent, ends []styledtext.SpanEnds, graphemes bool, out []spanProfile) []spanProfile {
	out = out[:0]
	at := 0
	for i, span := range spans {
		n := utf8.RuneCountInString(span.Content)
		sp := spanProfile{shift: gtx.Sp(span.Shift), box: span.Box != nil}
		if i < len(ends) {
			sp.truncator = ends[i].Truncator
		}
		if n > 0 && at+n <= len(adv) && at+n <= len(ext) {
			if span.Box != nil {
				sp.pieces = []piece{{body: adv[at], reach: ext[at]}}
			} else {
				joiner := styledtext.Extent{}
				if i < len(ends) {
					joiner = ends[i].Joiner
				}
				sp.pieces = s.cutSpan(span.Content, adv[at:at+n], ext[at:at+n], joiner, graphemes)
			}
		}
		at += n
		out = append(out, sp)
	}
	return out
}

func (s *profileScratch) cutSpan(content string, adv []fixed.Int26_6, ext []styledtext.Extent, joiner styledtext.Extent, graphemes bool) []piece {
	s.runes = append(s.runes[:0], []rune(content)...)
	s.runes = append(s.runes, wordJoiner)
	s.adv = append(append(s.adv[:0], adv...), 0)
	s.ext = append(append(s.ext[:0], ext...), joiner)
	s.seg.Init(s.runes)
	var out []piece
	add := func(offset int, text []rune, hard bool) {
		var p piece
		for i, r := range text {
			a := s.adv[offset+i]
			p.reach = p.reach.Union(s.ext[offset+i])
			if unicode.IsSpace(r) {
				p.trail += a
				if a != 0 {
					p.last = a
				}
				continue
			}
			p.body += p.trail + a
			p.trail, p.last = 0, 0
		}
		p.hard = hard
		out = append(out, p)
	}
	last := len(s.runes)
	if graphemes {
		it := s.seg.GraphemeIterator()
		for it.Next() {
			g := it.Grapheme()
			end := g.Offset + len(g.Text)
			add(g.Offset, g.Text, g.Text[len(g.Text)-1] == '\n' && end < last)
		}
		return out
	}
	it := s.seg.LineIterator()
	for it.Next() {
		l := it.Line()
		add(l.Offset, l.Text, l.IsMandatoryBreak && l.Offset+len(l.Text) < last)
	}
	return out
}

// rowProfileVersion is the version of the encoding of a profile, which
// changes with it: a profile of another reads as none.
const rowProfileVersion = 4

// profileWriter packs a profile into bytes, profileReader reads them back.
type profileWriter struct{ b []byte }

func (w *profileWriter) put(v int) { w.b = binary.AppendUvarint(w.b, uint64(max(v, 0))) }

type profileReader struct {
	b   []byte
	err error
}

func (r *profileReader) get() int {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.err = errProfile
		return 0
	}
	r.b = r.b[n:]
	return int(v)
}

// count reads how many of something follow, each of a byte at least.
func (r *profileReader) count() int {
	n := r.get()
	if n > len(r.b) {
		r.err = errProfile
		return 0
	}
	return n
}

// The flags of a profile: what it has, what is so of it.
const (
	profileText = 1 << iota
	profileOnText
	profileTextLast
	profileChips
	profilePlate
	profileAvatar
	profileBar
	profileReply
)

// The flags of a part, beside its kind in the low bits.
const (
	partEdge = 1 << (3 + iota)
	partFit
	partWide
	partOwnText
	partAlbum
)

// encode packs the profile into bytes for the cache: what it lacks is
// told by its flags, and each part keeps what its kind needs.
func (p *rowProfile) encode() []byte {
	w := &profileWriter{b: binary.AppendUvarint(nil, rowProfileVersion)}
	put := w.put
	bb := &p.bubble
	flags := 0
	for _, f := range []struct {
		on   bool
		flag int
	}{{p.text != nil, profileText}, {p.onText, profileOnText}, {bb.textLast, profileTextLast}, {len(p.chips) > 0, profileChips},
		{bb.plate != (image.Point{}), profilePlate}, {bb.slot != 0 || bb.avatar != 0, profileAvatar}, {bb.bar != 0 || bb.barNatural != 0, profileBar},
		{bb.reply != (replyProfile{}), profileReply}} {
		if f.on {
			flags |= f.flag
		}
	}
	put(flags)
	for _, v := range []int{p.width, p.height, p.bubbleWidth, p.footer.X, p.footer.Y, bb.rest} {
		put(v)
	}
	if flags&profileChips != 0 {
		put(len(p.chips))
		for _, c := range p.chips {
			put(c)
		}
		put(p.chipRow)
	}
	if flags&profilePlate != 0 {
		put(bb.plate.X)
		put(bb.plate.Y)
	}
	if flags&profileAvatar != 0 {
		put(bb.slot)
		put(bb.avatar)
	}
	if flags&profileBar != 0 {
		put(bb.bar)
		put(bb.barNatural)
	}
	if flags&profileReply != 0 {
		q := &bb.reply
		for _, v := range []int{q.named, q.line, q.lineHeight, q.pad, q.least, q.lines, q.size.X, q.size.Y} {
			put(v)
		}
	}
	put(len(bb.parts))
	for _, pp := range bb.parts {
		head := int(pp.kind)
		for _, f := range []struct {
			on   bool
			flag int
		}{{pp.edge, partEdge}, {pp.fit, partFit}, {pp.wide, partWide}, {pp.text != nil, partOwnText}, {pp.album != nil, partAlbum}} {
			if f.on {
				head |= f.flag
			}
		}
		put(head)
		switch {
		case pp.kind == partSpace:
			put(pp.size.Y)
		case pp.fit:
			// As large as mediaBox makes the picture.
			put(int(pp.media))
			put(pp.mw)
			put(pp.mh)
		case pp.album != nil:
			put(len(pp.album))
			for _, a := range pp.album {
				put(a.X)
				put(a.Y)
			}
		case pp.kind == partText, pp.kind == partReply, pp.kind == partReactions:
			// Of the text's profile, the quote's, the chips'.
		case pp.wide:
			put(pp.size.Y)
		default:
			put(pp.size.X)
			put(pp.size.Y)
		}
		if pp.text != nil {
			pp.text.encode(w)
		}
	}
	if p.text != nil {
		p.text.encode(w)
	}
	return w.b
}

// encode packs the text's profile.
func (t *textProfile) encode(w *profileWriter) {
	put := w.put
	put(t.gap)
	put(len(t.blocks))
	for _, block := range t.blocks {
		put(int(block.kind))
		put(block.inset)
		put(block.left)
		put(block.chrome)
		put(block.maxLines)
		put(len(block.spans))
		for _, span := range block.spans {
			// Most pieces of a span reach as far as each other and end with
			// the same spaces: those are the span's, and a piece tells its
			// own where they are others.
			type spaces struct{ trail, last fixed.Int26_6 }
			reaches, ends := map[styledtext.Extent]int{}, map[spaces]int{}
			base, end := styledtext.Extent{}, spaces{}
			for _, pc := range span.pieces {
				reaches[pc.reach]++
				if reaches[pc.reach] > reaches[base] {
					base = pc.reach
				}
				e := spaces{pc.trail, pc.last}
				ends[e]++
				if ends[e] > ends[end] {
					end = e
				}
			}
			flags := 0
			if span.box {
				flags = 1
			}
			put(flags)
			put(span.shift)
			put(span.truncator.Up)
			put(span.truncator.Down)
			put(base.Up)
			put(base.Down)
			put(int(end.trail))
			put(int(end.last))
			put(len(span.pieces))
			// Widths are kept as they are, in 64ths of a pixel: rounded, a
			// line that fills its width to a 64th wraps elsewhere. What is
			// not the span's follows them, by the pieces' places.
			var oddEnds, oddReaches []int
			for k, pc := range span.pieces {
				d := int(pc.body) << 1
				if pc.hard {
					d |= 1
				}
				put(d)
				if (spaces{pc.trail, pc.last}) != end {
					oddEnds = append(oddEnds, k)
				}
				if pc.reach != base {
					oddReaches = append(oddReaches, k)
				}
			}
			put(len(oddEnds))
			at := 0
			for _, k := range oddEnds {
				put(k - at)
				at = k
				put(int(span.pieces[k].trail))
				put(int(span.pieces[k].last))
			}
			put(len(oddReaches))
			at = 0
			for _, k := range oddReaches {
				put(k - at)
				at = k
				put(span.pieces[k].reach.Up)
				put(span.pieces[k].reach.Down)
			}
		}
	}
}

var errProfile = errors.New("ui: profile cut short")

// decodeRowProfile reads a profile encode made; ok is false for one of
// another version.
func decodeRowProfile(b []byte) (p rowProfile, ok bool, err error) {
	r := &profileReader{b: b}
	get, count := r.get, r.count
	if get() != rowProfileVersion || r.err != nil {
		return p, false, r.err
	}
	bb := &p.bubble
	flags := get()
	p.onText, bb.textLast = flags&profileOnText != 0, flags&profileTextLast != 0
	p.width, p.height, p.bubbleWidth = get(), get(), get()
	p.footer = image.Pt(get(), get())
	bb.rest = get()
	if flags&profileChips != 0 {
		p.chips = make([]int, count())
		for i := range p.chips {
			p.chips[i] = get()
		}
		p.chipRow = get()
	}
	if flags&profilePlate != 0 {
		bb.plate = image.Pt(get(), get())
	}
	if flags&profileAvatar != 0 {
		bb.slot, bb.avatar = get(), get()
	}
	if flags&profileBar != 0 {
		bb.bar, bb.barNatural = get(), get()
	}
	if flags&profileReply != 0 {
		q := &bb.reply
		q.named, q.line, q.lineHeight, q.pad, q.least, q.lines = get(), get(), get(), get(), get(), get()
		q.size = image.Pt(get(), get())
	}
	if n := count(); n > 0 {
		bb.parts = make([]partProfile, n)
		for i := range bb.parts {
			pp := &bb.parts[i]
			head := get()
			pp.kind = bubblePartKind(head & 7)
			pp.edge, pp.fit, pp.wide = head&partEdge != 0, head&partFit != 0, head&partWide != 0
			switch {
			case pp.kind == partSpace:
				pp.size.Y = get()
			case pp.fit:
				pp.media, pp.mw, pp.mh = model.MessageKind(get()), get(), get()
			case head&partAlbum != 0:
				pp.album = make([]image.Point, count())
				for k := range pp.album {
					pp.album[k] = image.Pt(get(), get())
				}
			case pp.kind == partText, pp.kind == partReply, pp.kind == partReactions:
			case pp.wide:
				pp.size.Y = get()
			default:
				pp.size = image.Pt(get(), get())
			}
			if head&partOwnText != 0 {
				pp.text = r.text()
			}
		}
	}
	if flags&profileText != 0 && r.err == nil {
		p.text = r.text()
	}
	return p, r.err == nil, r.err
}

// text reads a text's profile.
func (r *profileReader) text() *textProfile {
	get, count := r.get, r.count
	t := &textProfile{gap: get()}
	t.blocks = make([]blockProfile, count())
	for i := range t.blocks {
		block := &t.blocks[i]
		block.kind = blockKind(get())
		block.inset, block.left, block.chrome, block.maxLines = get(), get(), get(), get()
		block.spans = make([]spanProfile, count())
		for j := range block.spans {
			span := &block.spans[j]
			span.box = get()&1 != 0
			span.shift = get()
			span.truncator = styledtext.Extent{Up: get(), Down: get()}
			base := styledtext.Extent{Up: get(), Down: get()}
			trail, last := fixed.Int26_6(get()), fixed.Int26_6(get())
			span.pieces = make([]piece, count())
			for k := range span.pieces {
				pc := &span.pieces[k]
				d := get()
				pc.body = fixed.Int26_6(d >> 1)
				pc.hard = d&1 != 0
				pc.trail, pc.last, pc.reach = trail, last, base
			}
			// next is the next of the pieces told by their places, after at.
			var nowhere piece
			next := func(at *int) *piece {
				*at += get()
				if *at < 0 || *at >= len(span.pieces) {
					r.err = errProfile
					return &nowhere
				}
				return &span.pieces[*at]
			}
			at := 0
			for range count() {
				pc := next(&at)
				pc.trail, pc.last = fixed.Int26_6(get()), fixed.Int26_6(get())
			}
			at = 0
			for range count() {
				pc := next(&at)
				pc.reach = styledtext.Extent{Up: get(), Down: get()}
			}
		}
	}
	return t
}

// rowCapture is what a row's layout takes for its profile while on is set:
// in the frame the row is drawn in once it wants one (wantsProfile), and
// handed to the cache when the frame has its height (takeProfile).
type rowCapture struct {
	on bool
	// bubble is set once bubbleBody laid the row out; text is its text's.
	bubble  bool
	text    *textProfile
	footer  image.Point
	onText  bool
	chips   []int
	chipRow int
	// The bubble's (bubbleProfile), and the width of it with its comments
	// bar: bubbleWidth.
	parts           []partProfile
	plate           image.Point
	textLast        bool
	reply           replyProfile
	slot, avatar    int
	bar, barNatural int
	rest            int
	// took is how long cutting the text into pieces took; textMax is how
	// wide the text was let be.
	took    time.Duration
	textMax int
}

// textProfile gathers the blocks' parts of the profile richText took in
// gtx.
func (r *messageRow) textProfile(gtx layout.Context) *textProfile {
	t := &textProfile{gap: gtx.Dp(6)}
	for i := range r.textBlocks {
		b := &r.textBlocks[i]
		if b.end-b.first == 1 && r.runs[b.first].Block == 0 && r.runs[b.first].Text == "\n" && (b.trimStart || b.trimEnd) {
			continue
		}
		kind := blockFlow
		switch run := r.runs[b.first]; {
		case run.Pre:
			kind = blockCode
		case run.Quote && run.Collapsed:
			kind = blockFolded
		case run.Quote:
			kind = blockQuote
		}
		t.blocks = append(t.blocks, blockProfile{kind: kind, spans: b.spans, inset: gtx.Constraints.Max.X - b.flowMax, left: b.left, chrome: b.chrome, maxLines: b.foldLines})
	}
	return t
}

// wantsProfile is whether r's layout should take its profile: the history
// has none of the message as it is, and its profile tells its layout (not
// opaqueRow).
func (p *chatPage) wantsProfile(r *messageRow, m model.Message) bool {
	if r.profiled || p.profiled[m.Key.MessageID] == m.ContentRevision || p.opaqueRow(m) {
		return false
	}
	return true
}

// opaqueRow is whether how high m's row is at another width is beyond its
// profile: a service message, a bubbleless one, an album of files, a poll,
// an article, a preview of a page, buttons, a file, a message on its way or
// a draft a bot streams.
func (p *chatPage) opaqueRow(m model.Message) bool {
	return m.ServicePill() || p.unwrapped(m) || len(m.Attachments) > 1 && !tiledAlbum(m.Attachments) || m.Poll != nil || m.Todo != nil || m.Rich != nil || m.WebPage != nil || len(m.Buttons) > 0 || m.Streaming || m.Local() ||
		m.Media != nil && !boxedMedia(m.Kind)
}

// boxedMedia is whether media of kind are drawn as large as mediaBox tells,
// as a profile knows them at any width; a file's name is a label of two
// lines, which the profile does not wrap.
func boxedMedia(kind model.MessageKind) bool {
	switch kind {
	case model.MessagePhoto, model.MessageVideo, model.MessageGIF, model.MessageSticker:
		return true
	}
	return false
}

// takeProfile ends the capture of r's profile in the frame it was laid out
// width wide and height high, and encodes it; nil where the layout took
// less than the profile needs.
func (p *chatPage) takeProfile(r *messageRow, m model.Message, width, height int) []byte {
	c := &r.capture
	c.on = false
	defer r.dropCapture()
	if !c.bubble {
		return nil
	}
	for _, part := range c.parts {
		// A text the layout took no profile of.
		if part.kind == partText && part.text == nil && c.text == nil {
			return nil
		}
	}
	r.profiled = true
	prof := rowProfile{width: width, height: height, bubbleWidth: r.bodySize.X - c.slot, text: c.text, footer: c.footer, onText: c.onText, chips: c.chips, chipRow: c.chipRow,
		bubble: bubbleProfile{parts: c.parts, plate: c.plate, textLast: c.textLast, reply: c.reply, slot: c.slot, avatar: c.avatar, bar: c.bar, barNatural: c.barNatural, rest: c.rest}}
	data := prof.encode()
	p.profileStats.add(len(data), c.took)
	// The profile tells the layout it was taken from: one that does not
	// is told of in the journal, which a profile of another kind of row
	// shows.
	if h, w, exact := prof.predict(unit.Metric{PxPerDp: float32(p.env.ScaleMilli) / 1000, PxPerSp: float32(p.env.TextScaleMilli) / 1000}, width); h != height || exact && w != prof.bubbleWidth {
		p.profileStats.astray = append(p.profileStats.astray, fmt.Sprintf("%d: %dx%d, told %dx%d", m.Key.MessageID, prof.bubbleWidth, height, w, h))
	}
	return data
}

// retakeProfile has the row of message id take its profile again, in the
// next frame it is drawn in: its layout changed of itself since it took the
// last.
func (p *chatPage) retakeProfile(id model.MessageID) {
	delete(p.profiled, id)
	if r := p.rows[id]; r != nil {
		r.profiled = false
	}
}

// dropCapture lets go of what the row's layout took for its profile: the
// pieces, and what they were cut from, some 20 bytes a rune.
func (r *messageRow) dropCapture() {
	r.capture.text = nil
	for _, child := range r.album {
		child.dropCapture()
	}
	for i := range r.textBlocks {
		b := &r.textBlocks[i]
		b.spans = nil
		b.scratch.advances, b.scratch.extents, b.scratch.ends = nil, nil, nil
	}
}

// profileStats are the profiles a page took since the history's journal
// was told last (historylog): how many, their bytes, and how long cutting
// their texts took, for the largest too.
type profileStats struct {
	rows, bytes   int
	took, longest time.Duration
	// astray are the rows whose profiles did not tell their own layout.
	astray []string
}

func (s *profileStats) add(bytes int, took time.Duration) {
	s.rows++
	s.bytes += bytes
	s.took += took
	s.longest = max(s.longest, took)
}

// loadProfiled reads which messages the history has profiles of in env,
// when that is another than it read them in: rows of them take none.
func (p *chatPage) loadProfiled(env model.RenderEnvironment) {
	env.WidthPx = 0
	if p.profiled != nil && env == p.profileEnv {
		return
	}
	p.profileEnv = env
	p.profiled = nil
	for _, r := range p.rows {
		r.profiled = false
	}
	if source, ok := p.source.(model.RowProfiles); ok {
		p.profiled = source.Profiled(p.chat, env, rowProfileVersion)
	}
	if p.profiled == nil {
		p.profiled = map[model.MessageID]uint64{}
	}
}

// logProfiles tells the history's journal of the profiles taken since it
// was told last.
func (p *chatPage) logProfiles() {
	s := p.profileStats
	if s.rows == 0 {
		return
	}
	p.profileStats = profileStats{}
	historylog.Printf("profiles: chat %d: %d taken, %d bytes (%.0f a row), cutting %.1f µs a row, %.1f µs the longest", p.chat, s.rows, s.bytes, float64(s.bytes)/float64(s.rows), float64(s.took.Nanoseconds())/1000/float64(s.rows), float64(s.longest.Nanoseconds())/1000)
	if len(s.astray) > 0 {
		historylog.Printf("profiles: chat %d: %d do not tell their own rows (bubble, as told): %s", p.chat, len(s.astray), strings.Join(s.astray, "; "))
	}
}
