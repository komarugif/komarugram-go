// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/font"
	"gioui.org/text"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// An article is a rich message drawn as Telegram Desktop draws one (its
// engine in iv/markdown): headings, lists, quotes, code, tables, details,
// media and buttons, one under another. Its text is one sequence of runs,
// the texts of its blocks one after another, each after a line break that
// is not drawn, so that selection, copying, links, spoilers, dates and the
// colors of code work across blocks as in a message's text. A block's text
// is a leaf: a flow of those runs, a messageTextBlock, which textFlow sets
// as the block's role says.

// articleKind is what an article's block is.
type articleKind uint8

const (
	// articleFlow is text: a paragraph, a heading, a footer, an author's
	// line.
	articleFlow articleKind = iota
	articleCode
	// articleQuote is a quote of text, which a message's quote block draws,
	// or of blocks, drawn beside a bar; articlePullquote is set off in the
	// middle.
	articleQuote
	articlePullquote
	articleList
	articleDivider
	articleTable
	articleDetails
	articleMedia
	articleButtons
	articleMath
	// articleEmbedPost is a post embedded: its author and date over its
	// blocks.
	articleEmbedPost
	// articleCard is what the article names rather than shows: an embed, a
	// channel, a map, related articles, a block the client does not know.
	articleCard
	// articleAnchor is only a place a link goes to.
	articleAnchor
)

// articleBlock is a block of an article.
type articleBlock struct {
	kind articleKind
	// id tells the blocks apart for what the row keeps of them: whether
	// details are open, a slideshow's item, buttons.
	id int
	// leaf is the index of the block's text among the article's leaves;
	// title, of a table's, details' or related articles' title; caption, of
	// what is under it. -1 for none.
	leaf, title, caption int
	// skip is the space above the block, in dp, after another one.
	skip int
	// children are what a quote, details or a post embedded hold.
	children []*articleBlock
	items    []articleItem
	rows     []articleRow
	// columns is a table's count of them.
	columns           int
	bordered, striped bool
	media             []model.RichMedia
	slideshow         bool
	buttons           []model.RichButton
	align             string
	open              bool
	// card is what a card says, line by line, and the link it opens.
	card articleCardData
	// author and date head a post embedded.
	author string
	date   time.Time
	// anchors are the names of the anchors at the block's top.
	anchors []string
}

// articleItem is an item of a list: its marker, and its text or blocks.
type articleItem struct {
	marker            string
	checkbox, checked bool
	leaf              int
	blocks            []*articleBlock
	anchors           []string
}

// articleRow is a row of a table.
type articleRow struct {
	cells []articleCell
}

// articleCell is a cell of a table, its text a leaf.
type articleCell struct {
	leaf             int
	colspan, rowspan int
	header           bool
	align            text.Alignment
	valign           string
}

// articleCardData is what a card says: a title, lines under it, and the
// link a click opens.
type articleCardData struct {
	icon  string
	title string
	lines []string
	url   string
	// related are the related articles' own cards.
	related []articleCardData
}

// articleDoc is a rich message prepared for drawing.
type articleDoc struct {
	runs   []model.TextRun
	leaves []messageTextBlock
	blocks []*articleBlock
	// wide is set when a block takes the whole width, which the article
	// then takes too; otherwise it is as wide as its text.
	wide bool
	// datesDue is when a relative date of its text changes next.
	datesDue time.Time
	// runes counts the runes of runs.
	runes int
	ids   int
	// anchors are the names links to #name go to, each with the details
	// that hide it, the outermost first; the first anchor of a name is
	// the one links go to. details are the details around what is
	// prepared.
	anchors map[string][]*articleBlock
	// inline are the anchors inside a text, at the rune of the article's
	// text they are before: a link to one goes to its line.
	inline  map[string]int
	details []*articleBlock
	// part is set when Telegram sent the article cut short.
	part bool
	// tops are the page's blocks, where each is in its text, for copying
	// what is selected as HTML.
	tops []articleTop
	page model.RichPage
	l    localization.Catalog
	now  time.Time
}

// articleTop is a block of the page, not inside another: the runes of the
// article's text it holds, start to end, and the leaf of its text when it
// is all a text, which copying a part of cuts; -1 otherwise.
type articleTop struct {
	start, end int
	leaf       int
}

// Space above a block after another one, in dp, after Telegram Desktop's
// messageMarkdownBlockSkips, scaled from its 13 px text to the bubble's.
const (
	articleSkipParagraph = 5
	articleSkipHeading   = 12
	articleSkipBlock     = 10
	articleSkipChannel   = 7
)

// headingScale is the size of a heading of each level against the text's,
// as Telegram Desktop's messageMarkdownHeading styles are against its 13 px.
var headingScale = [7]float32{1, 19. / 13, 18. / 13, 17. / 13, 16. / 13, 15. / 13, 14. / 13}

// Styles of the article's flows.
var (
	articleBody     = flowStyle{}
	articleSmall    = flowStyle{scale: 12. / 13, dim: true}
	articleCaption  = flowStyle{scale: 12. / 13, dim: true}
	articleThinking = flowStyle{dim: true, italic: true}
	articleAuthor   = flowStyle{scale: 12. / 13, weight: font.SemiBold}
)

// prepareArticle prepares page to be drawn, its dates written as l writes
// them at now.
func prepareArticle(page model.RichPage, l localization.Catalog, now time.Time) *articleDoc {
	d := &articleDoc{l: l, now: now, part: page.Part, page: page}
	for _, b := range page.Blocks {
		start, leaves := d.runes, len(d.leaves)
		if a := d.blockOf(b); a != nil {
			d.blocks = append(d.blocks, a)
		}
		top := articleTop{start: start, end: d.runes, leaf: -1}
		if len(d.leaves) == leaves+1 && cuttable(b) {
			top.leaf = leaves
		}
		d.tops = append(d.tops, top)
	}
	return d
}

// cuttable reports whether b is all a text, the text of a leaf of its own
// as it is written: a part of it copies as itself.
func cuttable(b model.RichBlock) bool {
	switch b.Kind {
	case model.RichHeading, model.RichParagraph, model.RichFooter, model.RichThinking, model.RichCode:
	default:
		return false
	}
	for _, e := range b.Text.Entities {
		if e.Date != 0 {
			// A date is written as the reader's language writes it.
			return false
		}
	}
	return true
}

// mark takes the anchors of names at what is prepared, and returns them.
func (d *articleDoc) mark(names ...string) []string {
	var out []string
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := d.anchors[name]; !ok {
			if d.anchors == nil {
				d.anchors = map[string][]*articleBlock{}
			}
			d.anchors[name] = slices.Clone(d.details)
		}
		out = append(out, name)
	}
	return out
}

// leaf adds t as a leaf set in style, with extra entities over all of it,
// and returns its index; -1 for no text.
func (d *articleDoc) leaf(t model.RichText, style flowStyle, extra ...model.Entity) int {
	if t.Text == "" {
		return -1
	}
	units := model.UTF16Len(t.Text)
	entities := slices.Clone(t.Entities)
	for _, e := range extra {
		e.Length = units
		entities = append(entities, e)
	}
	text, entities := model.FormatDates(t.Text, entities, d.formatDate)
	runs := model.TextRuns(text, entities)
	if len(runs) == 0 {
		return -1
	}
	if len(d.runs) > 0 {
		// The line break between leaves is not drawn: it is what copying
		// puts between blocks.
		d.runs = append(d.runs, model.TextRun{Text: "\n"})
		d.runes++
	}
	b := messageTextBlock{first: len(d.runs), runeStart: d.runes, style: style}
	for i, name := range t.Anchors {
		if at := t.AnchorOffset(i); at > 0 && name != "" {
			if _, ok := d.inline[name]; !ok {
				if d.inline == nil {
					d.inline = map[string]int{}
				}
				d.inline[name] = d.runes + runesBefore(t.Text, at)
			}
		}
	}
	for _, r := range runs {
		d.runes += utf8.RuneCountInString(r.Text)
	}
	d.runs = append(d.runs, runs...)
	b.end = len(d.runs)
	d.leaves = append(d.leaves, b)
	return len(d.leaves) - 1
}

// plain adds s as a leaf without entities.
func (d *articleDoc) plain(s string, style flowStyle) int {
	return d.leaf(model.RichText{Text: s}, style)
}

func (d *articleDoc) formatDate(e model.Entity) string {
	if e.Date == 0 {
		return ""
	}
	s, due := formattedDate(d.l, time.Unix(e.Date, 0), d.now, e.DateFormat)
	if !due.IsZero() && (d.datesDue.IsZero() || due.Before(d.datesDue)) {
		d.datesDue = due
	}
	return s
}

func (d *articleDoc) block(kind articleKind, skip int) *articleBlock {
	d.ids++
	return &articleBlock{kind: kind, id: d.ids, leaf: -1, title: -1, caption: -1, skip: skip}
}

func (d *articleDoc) blocksOf(blocks []model.RichBlock) []*articleBlock {
	var out []*articleBlock
	for _, b := range blocks {
		if a := d.blockOf(b); a != nil {
			out = append(out, a)
		}
	}
	return out
}

// blockOf prepares b, as Telegram Desktop's PrepareNativeIvBlock does; nil
// for a block with nothing to show.
func (d *articleDoc) blockOf(b model.RichBlock) *articleBlock {
	var a *articleBlock
	switch b.Kind {
	case model.RichHeading:
		a = d.block(articleFlow, articleSkipHeading)
		level := min(max(b.Level, 1), 6)
		a.leaf = d.leaf(b.Text, flowStyle{scale: headingScale[level], weight: font.SemiBold})
	case model.RichParagraph:
		a = d.block(articleFlow, articleSkipParagraph)
		a.leaf = d.leaf(b.Text, articleBody)
	case model.RichFooter:
		a = d.block(articleFlow, articleSkipParagraph)
		a.leaf = d.leaf(b.Text, articleSmall)
	case model.RichThinking:
		a = d.block(articleFlow, articleSkipParagraph)
		a.leaf = d.leaf(b.Text, articleThinking)
	case model.RichAuthorDate:
		a = d.block(articleFlow, articleSkipParagraph)
		line := b.Text
		if !b.Date.IsZero() {
			if line.Text != "" {
				line.Append(model.RichText{Text: " · "})
			}
			line.Append(model.RichText{Text: dayOfMonth(d.l, b.Date.Local(), d.now, true)})
		}
		a.leaf = d.leaf(line, articleSmall)
	case model.RichCode:
		a = d.block(articleCode, articleSkipBlock)
		a.leaf = d.leaf(b.Text, articleBody, model.Entity{Kind: "pre", Language: b.Language})
		d.wide = true
	case model.RichMath:
		a = d.block(articleMath, articleSkipBlock)
		a.leaf = d.leaf(model.RichText{Text: b.Formula}, flowStyle{align: text.Middle}, model.Entity{Kind: "math"})
		d.wide = true
	case model.RichDivider:
		a = d.block(articleDivider, articleSkipBlock)
		d.wide = true
	case model.RichAnchor:
		a = d.block(articleAnchor, 0)
	case model.RichQuote:
		a = d.quote(b)
	case model.RichList:
		a = d.list(b)
	case model.RichTable:
		a = d.table(b)
	case model.RichDetails:
		a = d.block(articleDetails, articleSkipBlock)
		a.title = d.leaf(b.Text, flowStyle{weight: font.SemiBold})
		d.details = append(d.details, a)
		a.children = d.blocksOf(b.Blocks)
		d.details = d.details[:len(d.details)-1]
		a.open = b.Open
		d.wide = true
	case model.RichMediaBlock:
		a = d.block(articleMedia, articleSkipBlock)
		for _, m := range b.Media {
			if m.Media != nil {
				a.media = append(a.media, m)
			}
		}
		a.slideshow = b.Slideshow && len(a.media) > 1
		a.caption = d.leaf(b.Caption, articleCaption)
		d.wide = true
		if len(a.media) == 0 && a.caption < 0 {
			return nil
		}
	case model.RichButtons:
		a = d.block(articleButtons, articleSkipBlock)
		a.buttons, a.align = b.Buttons, b.Align
		d.wide = d.wide || b.Align == ""
	case model.RichEmbedPost:
		a = d.block(articleEmbedPost, articleSkipBlock)
		a.author, a.date = b.Author, b.Date
		a.children = d.blocksOf(b.Blocks)
		a.caption = d.leaf(b.Caption, articleCaption)
		d.wide = true
	default:
		a = d.card(b)
	}
	if a == nil {
		return nil
	}
	a.anchors = d.mark(slices.Concat([]string{b.Anchor}, b.Text.Anchors, b.Caption.Anchors)...)
	if a.kind == articleFlow && a.leaf < 0 && len(a.anchors) == 0 {
		return nil
	}
	return a
}

// quote prepares a quote: of text, as a message's quote block draws it,
// collapsible; of blocks, beside a bar; or a pullquote, in the middle.
func (d *articleDoc) quote(b model.RichBlock) *articleBlock {
	if b.Pullquote {
		a := d.block(articlePullquote, articleSkipBlock)
		a.leaf = d.leaf(b.Text, flowStyle{italic: true, align: text.Middle})
		a.children = d.blocksOf(b.Blocks)
		a.caption = d.leaf(b.Caption, flowStyle{scale: 12. / 13, dim: true, align: text.Middle})
		return a
	}
	a := d.block(articleQuote, articleSkipBlock)
	if len(b.Blocks) == 0 {
		a.leaf = d.leaf(b.Text, articleBody, model.Entity{Kind: "quote", Collapsed: b.Collapsed})
	} else {
		if text := d.leaf(b.Text, articleBody); text >= 0 {
			flow := d.block(articleFlow, articleSkipParagraph)
			flow.leaf = text
			a.children = append(a.children, flow)
		}
		a.children = append(a.children, d.blocksOf(b.Blocks)...)
	}
	a.caption = d.leaf(b.Caption, articleAuthor)
	d.wide = true
	return a
}

// list prepares a list with its items' markers.
func (d *articleDoc) list(b model.RichBlock) *articleBlock {
	a := d.block(articleList, articleSkipParagraph)
	markers := b.ListMarkers()
	for i, item := range b.Items {
		it := articleItem{marker: markers[i], checkbox: item.Checkbox, checked: item.Checked, leaf: -1}
		it.anchors = d.mark(slices.Concat([]string{item.Anchor}, item.Text.Anchors)...)
		if item.Text.Text != "" {
			it.leaf = d.leaf(item.Text, articleBody)
		} else {
			it.blocks = d.blocksOf(item.Blocks)
		}
		a.items = append(a.items, it)
	}
	if len(a.items) == 0 {
		return nil
	}
	return a
}

// table prepares a table: its title, and its cells as leaves.
func (d *articleDoc) table(b model.RichBlock) *articleBlock {
	a := d.block(articleTable, articleSkipBlock)
	a.title = d.leaf(b.Text, articleCaption)
	a.bordered, a.striped = b.Bordered, b.Striped
	for _, row := range b.Rows {
		var r articleRow
		for _, cell := range row.Cells {
			style := articleBody
			if cell.Header {
				style.weight = font.SemiBold
			}
			c := articleCell{colspan: max(cell.Colspan, 1), rowspan: max(cell.Rowspan, 1), header: cell.Header, valign: cell.VAlign}
			switch cell.Align {
			case "center":
				c.align = text.Middle
			case "right":
				c.align = text.End
			}
			style.align = c.align
			c.leaf = d.leaf(cell.Text, style)
			r.cells = append(r.cells, c)
		}
		a.rows = append(a.rows, r)
	}
	a.columns = tableColumns(a.rows)
	if a.columns == 0 {
		return nil
	}
	d.wide = true
	return a
}

// card prepares what the article names rather than shows.
func (d *articleDoc) card(b model.RichBlock) *articleBlock {
	l := d.l
	skip := articleSkipBlock
	var c articleCardData
	switch b.Kind {
	case model.RichEmbed:
		c = articleCardData{icon: "link", title: l.T("rich.click_to_view"), url: b.URL}
		if b.URL != "" {
			c.lines = []string{b.URL}
		}
	case model.RichChannel:
		c = articleCardData{icon: "channel", title: b.Title}
		if b.Username != "" {
			c.lines = []string{"@" + b.Username}
			c.url = "https://t.me/" + b.Username
		}
		skip = articleSkipChannel
	case model.RichMap:
		c = articleCardData{icon: "place", title: l.T("rich.map"), lines: []string{fmt.Sprintf("%.5f, %.5f", b.Latitude, b.Longitude)}}
	case model.RichRelated:
		c = articleCardData{icon: "link"}
		for _, r := range b.Related {
			related := articleCardData{title: r.Title, url: r.URL}
			if r.Description != "" {
				related.lines = append(related.lines, r.Description)
			}
			var by []string
			if r.Author != "" {
				by = append(by, r.Author)
			}
			if !r.Date.IsZero() {
				by = append(by, dayOfMonth(l, r.Date.Local(), d.now, true))
			}
			if len(by) > 0 {
				related.lines = append(related.lines, strings.Join(by, " · "))
			}
			c.related = append(c.related, related)
		}
		if len(c.related) == 0 {
			return nil
		}
	default:
		c = articleCardData{icon: "info", title: l.T("rich.unsupported_title"), lines: []string{l.T("rich.unsupported_text")}}
	}
	a := d.block(articleCard, skip)
	a.card = c
	if b.Kind == model.RichRelated {
		a.title = d.leaf(b.Text, articleCaption)
	}
	a.caption = d.leaf(b.Caption, articleCaption)
	d.wide = true
	return a
}

// tableColumns counts the columns of rows, cells spanning rows and columns
// taking their places as an HTML table's do.
func tableColumns(rows []articleRow) int {
	columns := 0
	for _, row := range tablePlaces(rows) {
		for _, p := range row {
			columns = max(columns, p.column+p.colspan)
		}
	}
	return columns
}

// tablePlace is where a cell is in its table's grid.
type tablePlace struct {
	row, column, colspan, rowspan int
}

// tablePlaces places each cell of rows in the grid, row by row: a cell
// takes the first column a cell above it does not span, as in HTML. The
// spans are bounded by the table.
func tablePlaces(rows []articleRow) [][]tablePlace {
	// taken counts, for each column, the rows still spanned from above.
	var taken []int
	places := make([][]tablePlace, len(rows))
	for i, row := range rows {
		column := 0
		for _, cell := range row.cells {
			for column < len(taken) && taken[column] > 0 {
				column++
			}
			p := tablePlace{row: i, column: column, colspan: min(cell.colspan, 64), rowspan: min(cell.rowspan, len(rows)-i)}
			for len(taken) < column+p.colspan {
				taken = append(taken, 0)
			}
			for c := column; c < column+p.colspan; c++ {
				taken[c] = max(taken[c], p.rowspan)
			}
			places[i] = append(places[i], p)
			column += p.colspan
		}
		for c := range taken {
			if taken[c] > 0 {
				taken[c]--
			}
		}
	}
	return places
}

// runesBefore counts the runes of s before UTF-16 offset at.
func runesBefore(s string, at int) int {
	n, units := 0, 0
	for _, r := range s {
		if units >= at {
			break
		}
		units++
		if r >= 0x10000 {
			units++
		}
		n++
	}
	return n
}
