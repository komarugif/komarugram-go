// SPDX-License-Identifier: Unlicense OR MIT

// Package markdown prepares a Markdown file as an article: the blocks of a
// model.RichPage, which the client draws as it draws a rich message.
//
// It reads Telegram's dialect as Telegram Desktop's viewer does
// (iv/markdown): GitHub Flavored Markdown through cmark-gfm (pkg/cmark),
// with footnotes; formulas in dollars, found outside code and HTML by
// Telegram Desktop's rules, and in ```math blocks; ==marks== and
// ||spoilers||; <details> with its <summary>, and <u>, <ins>, <sub>, <sup>,
// <mark> and <tg-spoiler> among the HTML in the text. Headings get anchors
// as Telegram Desktop names them, footnotes are numbered as they are
// referred to and listed at the end, and links go to anchors, to the web,
// or to another Markdown file beside this one. Images show their
// description: a file's images are not loaded, as in Telegram Desktop.
package markdown

import (
	"bytes"
	"context"
	"errors"
	"html"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/cmark"
)

// Limits of formulas, Telegram Desktop's (ParseLimitsForIv): a formula
// longer, or one past the last, stays text.
const (
	maxFormulaBytes = 64 << 10
	maxFormulas     = 10_000
)

// placeholder is the first character that stands for a formula in the
// source cmark parses a second time: one character of a private use plane
// for each, which cmark takes for a letter.
const placeholder = 0xF0000

// Parser parses Markdown into cmark's tree: a cmark.Runtime's Parse.
type Parser func(ctx context.Context, source []byte) (*cmark.Node, error)

// Document is a Markdown file prepared as an article.
type Document struct {
	Page model.RichPage
	// Title is the text of the first heading, empty without one.
	Title string
}

// ErrEmpty is a file with nothing to show.
var ErrEmpty = errors.New("markdown: the file is empty")

// Prepare prepares source, the Markdown file at path, which relative links
// are resolved against, parsing it with parse.
func Prepare(ctx context.Context, parse Parser, source []byte, path string) (Document, error) {
	src := normalize(source)
	// The first parse finds the code and the HTML, where dollars are not
	// formulas; the second parses the source with each formula replaced
	// by its placeholder, so that cmark leaves what is in it alone.
	tree, err := parse(ctx, src)
	if err != nil {
		return Document{}, err
	}
	starts := lineStarts(src)
	mask := make([]bool, len(src))
	maskCode(tree, starts, mask)
	formulas, replaced := extractMath(src, mask)
	if len(formulas) > 0 {
		if tree, err = parse(ctx, replaced); err != nil {
			return Document{}, err
		}
	}
	c := &converter{formulas: formulas, dir: filepath.Dir(path), anchors: map[string]int{}, footnotes: map[string]int{}}
	c.collectFootnotes(tree)
	page := model.RichPage{Blocks: c.blocks(tree.Children)}
	page.Blocks = append(page.Blocks, c.footnoteBlocks()...)
	if len(page.Blocks) == 0 {
		return Document{}, ErrEmpty
	}
	return Document{Page: page, Title: c.title}, nil
}

// normalize makes source valid UTF-8 without a byte order mark, with LF
// line ends, and without characters of the placeholders' plane.
func normalize(source []byte) []byte {
	s := strings.ToValidUTF8(string(source), "�")
	s = strings.TrimPrefix(s, "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r == 0 || r >= placeholder && r <= 0xFFFFD {
			return '�'
		}
		return r
	}, s)
	return []byte(s)
}

// lineStarts are the offsets the lines of src start at.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// offset is the byte offset of cmark's line and column, both 1-based, in
// src: -1 when it does not know them.
func offset(starts []int, size, line, column int) int {
	if line < 1 || column < 1 || line > len(starts) {
		return -1
	}
	return min(starts[line-1]+column-1, size)
}

// maskCode marks the bytes of code and HTML in n's tree.
func maskCode(n *cmark.Node, starts []int, mask []bool) {
	switch n.Kind {
	case cmark.Code, cmark.CodeBlock, cmark.HTMLInline, cmark.HTMLBlock:
		from := offset(starts, len(mask), n.StartLine, n.StartColumn)
		to := offset(starts, len(mask), n.EndLine, n.EndColumn)
		if from >= 0 && to >= from {
			for i := from; i <= to && i < len(mask); i++ {
				mask[i] = true
			}
		}
		return
	}
	for _, child := range n.Children {
		maskCode(child, starts, mask)
	}
}

// formula is a formula found in the source.
type formula struct {
	tex     string
	display bool
}

// extractMath finds the formulas of src outside what mask marks, as
// Telegram Desktop does (ExtractMathRegions): $$display$$, across lines,
// and $inline$ within a line, a $ escaped by a backslash being none,
// amounts of money and prose between two dollars neither. It returns them
// and src with each replaced by its placeholder.
func extractMath(src []byte, mask []bool) ([]formula, []byte) {
	var formulas []formula
	var out bytes.Buffer
	last := 0
	for i := 0; i < len(src); i++ {
		if mask[i] || src[i] != '$' || i > 0 && src[i-1] == '\\' {
			continue
		}
		display := i+1 < len(src) && src[i+1] == '$' && !mask[i+1]
		size := 1
		if display {
			size = 2
		}
		closing := -1
		for j := i + size; j < len(src); j++ {
			if mask[j] {
				continue
			}
			if src[j] == '\\' {
				j++
				continue
			}
			if !display && src[j] == '\n' {
				break
			}
			if src[j] == '$' && (!display || j+1 < len(src) && src[j+1] == '$' && !mask[j+1]) {
				closing = j
				break
			}
		}
		if closing < 0 {
			continue
		}
		content := src[i+size : closing]
		if !display && (looksLikeCurrency(content) || !hasMathSignal(content) && looksLikeProse(content)) {
			continue
		}
		if display {
			content = stripQuoteMarkers(content)
		}
		if len(content) > maxFormulaBytes || len(formulas) == maxFormulas || len(bytes.TrimSpace(content)) == 0 {
			continue
		}
		out.Write(src[last:i])
		out.WriteRune(rune(placeholder + len(formulas)))
		formulas = append(formulas, formula{tex: strings.TrimSpace(string(content)), display: display})
		last = closing + size
		i = last - 1
	}
	out.Write(src[last:])
	return formulas, out.Bytes()
}

// looksLikeCurrency is content of digits, dots, commas and spaces, as
// between "$5 and $10".
func looksLikeCurrency(content []byte) bool {
	digit := false
	for _, c := range content {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c != '.' && c != ',' && c != ' ':
			return false
		}
	}
	return digit
}

// hasMathSignal is content with a command, or ^, _, +, =, < or > between
// two things.
func hasMathSignal(content []byte) bool {
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
	between := func(i int) bool {
		l, r := i-1, i+1
		for l >= 0 && space(content[l]) {
			l--
		}
		for r < len(content) && space(content[r]) {
			r++
		}
		return l >= 0 && r < len(content)
	}
	for i, c := range content {
		switch c {
		case '\\':
			if i+1 < len(content) {
				n := content[i+1]
				if isLetter(n) || n == ',' || n == ':' || n == ';' || n == '!' {
					return true
				}
			}
		case '^', '_', '+', '=', '<', '>':
			if between(i) {
				return true
			}
		}
	}
	return false
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// looksLikeProse is content of two words or more, or of one of five
// letters, not commands.
func looksLikeProse(content []byte) bool {
	words, longest := 0, 0
	for i := 0; i < len(content); {
		for i < len(content) && !isLetter(content[i]) {
			i++
		}
		start := i
		for i < len(content) && isLetter(content[i]) {
			i++
		}
		if n := i - start; n >= 2 && (start == 0 || content[start-1] != '\\') {
			words++
			longest = max(longest, n)
		}
	}
	return words >= 2 || longest >= 5
}

// stripQuoteMarkers takes the markers of block quotes off the lines of a
// display formula after its first.
func stripQuoteMarkers(content []byte) []byte {
	lines := bytes.Split(content, []byte("\n"))
	for i := 1; i < len(lines); i++ {
		l := lines[i]
		for {
			t := bytes.TrimLeft(l, " \t")
			if len(t) == 0 || t[0] != '>' {
				break
			}
			l = t[1:]
		}
		lines[i] = bytes.TrimPrefix(l, []byte(" "))
	}
	return bytes.Join(lines, []byte("\n"))
}

// converter turns cmark's tree into blocks.
type converter struct {
	formulas []formula
	dir      string
	title    string
	// anchors counts the headings' anchors, to tell duplicates apart.
	anchors map[string]int
	// footnotes are the definitions by label, and numbers the ones
	// referred to, in the order of their first references.
	definitions map[string]*cmark.Node
	footnotes   map[string]int
	order       []string
	referred    map[int]bool
}

func (c *converter) collectFootnotes(n *cmark.Node) {
	if n.Kind == cmark.FootnoteDefinition {
		if c.definitions == nil {
			c.definitions = map[string]*cmark.Node{}
		}
		if _, ok := c.definitions[n.Literal]; !ok {
			c.definitions[n.Literal] = n
		}
		return
	}
	for _, child := range n.Children {
		c.collectFootnotes(child)
	}
}

// blocks converts nodes, block nodes, one after another: <details> in
// HTML takes the blocks up to its </details>.
func (c *converter) blocks(nodes []*cmark.Node) []model.RichBlock {
	var out []model.RichBlock
	for i := 0; i < len(nodes); i++ {
		n := nodes[i]
		if n.Kind == cmark.HTMLBlock && opensDetails(n.Literal) {
			end := closingDetails(nodes, i)
			out = append(out, c.details(n.Literal, nodes[i+1:end]))
			i = end
			continue
		}
		out = append(out, c.block(n)...)
	}
	return out
}

func opensDetails(literal string) bool {
	t := strings.ToLower(strings.TrimSpace(literal))
	return strings.HasPrefix(t, "<details") && !strings.Contains(t, "</details>")
}

// closingDetails is the index of the HTML block that closes the details
// nodes[open] opens, len(nodes) when none does.
func closingDetails(nodes []*cmark.Node, open int) int {
	depth := 0
	for i := open; i < len(nodes); i++ {
		if nodes[i].Kind != cmark.HTMLBlock {
			continue
		}
		t := strings.ToLower(nodes[i].Literal)
		depth += strings.Count(t, "<details") - strings.Count(t, "</details>")
		if depth <= 0 {
			return i
		}
	}
	return len(nodes)
}

// details is the details an HTML block opens, its summary in it, over
// inner, the blocks up to its end.
func (c *converter) details(opening string, inner []*cmark.Node) model.RichBlock {
	b := model.RichBlock{Kind: model.RichDetails}
	lower := strings.ToLower(opening)
	tag := lower[:strings.IndexByte(lower+">", '>')]
	b.Open = strings.Contains(tag, " open")
	summary := "Details"
	if from := strings.Index(lower, "<summary"); from >= 0 {
		if gt := strings.IndexByte(lower[from:], '>'); gt >= 0 {
			rest := opening[from+gt+1:]
			if to := strings.Index(strings.ToLower(rest), "</summary>"); to >= 0 {
				rest = rest[:to]
			}
			if s := htmlText(rest); s != "" {
				summary = s
			}
		}
	}
	b.Text = model.RichText{Text: summary}
	b.Blocks = c.blocks(inner)
	return b
}

// htmlText is the text of HTML: its tags left out, its entities decoded,
// its runs of white space one space.
func htmlText(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			b.WriteByte(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
}

// block converts a block node.
func (c *converter) block(n *cmark.Node) []model.RichBlock {
	switch n.Kind {
	case cmark.Paragraph:
		return c.paragraph(n)
	case cmark.Heading:
		t := c.text(n.Children)
		anchor := c.anchor(t.Text)
		if c.title == "" {
			c.title = strings.TrimSpace(t.Text)
		}
		return []model.RichBlock{{Kind: model.RichHeading, Level: min(max(n.Level, 1), 6), Text: t, Anchor: anchor}}
	case cmark.BlockQuote:
		return []model.RichBlock{{Kind: model.RichQuote, Blocks: c.blocks(n.Children)}}
	case cmark.List:
		return []model.RichBlock{c.list(n)}
	case cmark.CodeBlock:
		language, _, _ := strings.Cut(strings.TrimSpace(n.Info), " ")
		code := strings.TrimSuffix(n.Literal, "\n")
		if strings.EqualFold(language, "math") {
			return []model.RichBlock{{Kind: model.RichMath, Formula: strings.TrimSpace(code)}}
		}
		// The fonts of code have no glyph for a tab: four spaces stand
		// for it.
		code = strings.ReplaceAll(code, "\t", "    ")
		return []model.RichBlock{{Kind: model.RichCode, Language: language, Text: model.RichText{Text: code}}}
	case cmark.HTMLBlock:
		if t := htmlText(n.Literal); t != "" {
			return []model.RichBlock{{Kind: model.RichParagraph, Text: model.RichText{Text: t}}}
		}
		return nil
	case cmark.ThematicBreak:
		return []model.RichBlock{{Kind: model.RichDivider}}
	case cmark.Table:
		return []model.RichBlock{c.table(n)}
	case cmark.FootnoteDefinition:
		// Listed at the end, in the order of their references.
		return nil
	default:
		return c.blocks(n.Children)
	}
}

// paragraph converts a paragraph: a display formula alone among its
// inlines is a math block of its own between what is around it.
func (c *converter) paragraph(n *cmark.Node) []model.RichBlock {
	var out []model.RichBlock
	var t model.RichText
	tags := &htmlTags{}
	flush := func() {
		trimText(&t)
		if t.Text != "" || len(t.Anchors) > 0 {
			out = append(out, model.RichBlock{Kind: model.RichParagraph, Text: t})
		}
		t = model.RichText{}
		tags = &htmlTags{}
	}
	for _, child := range n.Children {
		if child.Kind == cmark.Text {
			rest := child.Literal
			for {
				i, f, ok := c.displayIn(rest)
				if !ok {
					break
				}
				c.literal(&t, rest[:i])
				flush()
				out = append(out, model.RichBlock{Kind: model.RichMath, Formula: f.tex})
				rest = rest[i+utf8.RuneLen(rune(placeholder)):]
			}
			c.literal(&t, rest)
			continue
		}
		c.inline(&t, child, tags)
	}
	flush()
	return out
}

// displayIn finds the first display formula's placeholder in s.
func (c *converter) displayIn(s string) (int, formula, bool) {
	for i, r := range s {
		if f, ok := c.formula(r); ok && f.display {
			return i, f, true
		}
	}
	return 0, formula{}, false
}

func (c *converter) formula(r rune) (formula, bool) {
	if i := int(r) - placeholder; i >= 0 && i < len(c.formulas) {
		return c.formulas[i], true
	}
	return formula{}, false
}

// trimText takes white space off the ends of t, its entities moved and
// cut with it.
func trimText(t *model.RichText) {
	trimmed := strings.TrimLeftFunc(t.Text, unicode.IsSpace)
	shift := model.UTF16Len(t.Text) - model.UTF16Len(trimmed)
	trimmed = strings.TrimRightFunc(trimmed, unicode.IsSpace)
	size := model.UTF16Len(trimmed)
	entities := t.Entities[:0]
	for _, e := range t.Entities {
		from, to := max(e.Offset-shift, 0), min(e.Offset+e.Length-shift, size)
		if to > from {
			e.Offset, e.Length = from, to-from
			entities = append(entities, e)
		}
	}
	for i, at := range t.AnchorAt {
		if at >= 0 {
			t.AnchorAt[i] = min(max(at-shift, 0), size)
		}
	}
	t.Text, t.Entities = trimmed, entities
}

// text converts inline nodes.
func (c *converter) text(nodes []*cmark.Node) model.RichText {
	var t model.RichText
	tags := &htmlTags{}
	for _, n := range nodes {
		c.inline(&t, n, tags)
	}
	trimText(&t)
	return t
}

// htmlTags are the HTML tags opened in a text and not closed yet, with
// where in it they were.
type htmlTags struct {
	open []openTag
}

type openTag struct {
	name string
	from int
}

// tagEntity is the entity an HTML tag of the text gives.
var tagEntity = map[string]string{
	"u": "underline", "ins": "underline",
	"sub": "sub", "sup": "sup",
	"s": "strike", "del": "strike", "strike": "strike",
	"b": "bold", "strong": "bold",
	"i": "italic", "em": "italic",
	"mark": "marked", "code": "code",
	"tg-spoiler": "spoiler",
}

// tag applies the HTML of an inline node to t.
func (h *htmlTags) tag(t *model.RichText, literal string) {
	s := strings.ToLower(strings.TrimSpace(literal))
	if !strings.HasPrefix(s, "<") || !strings.HasSuffix(s, ">") {
		return
	}
	s = strings.Trim(s, "<>/ ")
	closing := strings.HasPrefix(strings.TrimSpace(literal)[1:], "/")
	name, attrs, _ := strings.Cut(s, " ")
	if name == "br" {
		t.Text += "\n"
		return
	}
	kind, ok := tagEntity[name]
	if name == "span" && strings.Contains(attrs, "tg-spoiler") {
		kind, ok = "spoiler", true
	}
	if !ok {
		return
	}
	if !closing {
		h.open = append(h.open, openTag{name: kind, from: model.UTF16Len(t.Text)})
		return
	}
	for i := len(h.open) - 1; i >= 0; i-- {
		if h.open[i].name == kind {
			t.Mark(h.open[i].from, model.Entity{Kind: kind})
			h.open = append(h.open[:i], h.open[i+1:]...)
			return
		}
	}
}

// inline converts an inline node into t.
func (c *converter) inline(t *model.RichText, n *cmark.Node, tags *htmlTags) {
	from := model.UTF16Len(t.Text)
	mark := func(kind string) { t.Mark(from, model.Entity{Kind: kind}) }
	children := func() {
		for _, child := range n.Children {
			c.inline(t, child, tags)
		}
	}
	switch n.Kind {
	case cmark.Text:
		c.literal(t, n.Literal)
	case cmark.SoftBreak:
		t.Text += " "
	case cmark.LineBreak:
		t.Text += "\n"
	case cmark.Code:
		t.Text += n.Literal
		mark("code")
	case cmark.HTMLInline:
		tags.tag(t, n.Literal)
	case cmark.Emphasis:
		children()
		mark("italic")
	case cmark.Strong:
		children()
		mark("bold")
	case cmark.Strikethrough:
		children()
		mark("strike")
	case cmark.Link:
		children()
		if u := c.link(n.URL); u != "" {
			t.Mark(from, model.Entity{Kind: "url", URL: u})
		}
	case cmark.Image:
		children()
		if model.UTF16Len(t.Text) == from {
			t.Text += n.Title
		}
		if u := c.link(n.URL); u != "" {
			t.Mark(from, model.Entity{Kind: "url", URL: u})
		}
	case cmark.FootnoteReference:
		number, first := c.reference(n.Literal)
		if number == 0 {
			t.Text += "[^" + n.Literal + "]"
			return
		}
		if first {
			t.AddAnchor("fnref-" + strconv.Itoa(number))
		}
		t.Text += strconv.Itoa(number)
		mark("sup")
		t.Mark(from, model.Entity{Kind: "url", URL: "#fn-" + strconv.Itoa(number)})
	default:
		children()
	}
}

// reference numbers the footnote label refers to, 0 when it has no
// definition, and tells whether this is its first reference.
func (c *converter) reference(label string) (int, bool) {
	if c.definitions[label] == nil {
		return 0, false
	}
	if n, ok := c.footnotes[label]; ok {
		return n, false
	}
	c.order = append(c.order, label)
	n := len(c.order)
	c.footnotes[label] = n
	return n, true
}

// footnoteBlocks list the footnotes referred to, under a divider: each
// with its anchor, and a link back to its first reference.
func (c *converter) footnoteBlocks() []model.RichBlock {
	if len(c.order) == 0 {
		return nil
	}
	list := model.RichBlock{Kind: model.RichList, Ordered: true}
	// Definitions may refer to footnotes not referred to yet: they are
	// numbered as they come.
	for i := 0; i < len(c.order); i++ {
		number := i + 1
		blocks := c.blocks(c.definitions[c.order[i]].Children)
		back := model.RichText{Text: " ↩", Entities: []model.Entity{{Kind: "url", Offset: 1, Length: 1, URL: "#fnref-" + strconv.Itoa(number)}}}
		item := model.RichListItem{Anchor: "fn-" + strconv.Itoa(number)}
		if len(blocks) == 1 && blocks[0].Kind == model.RichParagraph {
			item.Text = blocks[0].Text
			item.Text.Append(back)
		} else {
			item.Blocks = append(blocks, model.RichBlock{Kind: model.RichParagraph, Text: model.RichText{Text: "↩", Entities: []model.Entity{{Kind: "url", Offset: 0, Length: 1, URL: "#fnref-" + strconv.Itoa(number)}}}})
		}
		list.Items = append(list.Items, item)
	}
	return []model.RichBlock{{Kind: model.RichDivider}, list}
}

// literal adds text to t: its formulas' placeholders as formulas, and
// ==marks== and ||spoilers|| marked.
func (c *converter) literal(t *model.RichText, s string) {
	for s != "" {
		i, delim := nextMark(s)
		if i < 0 {
			c.withFormulas(t, s)
			return
		}
		end := strings.Index(s[i+2:], delim)
		inner := ""
		if end >= 0 {
			inner = s[i+2 : i+2+end]
		}
		if end < 0 || inner == "" || strings.TrimSpace(inner) != inner {
			c.withFormulas(t, s[:i+2])
			s = s[i+2:]
			continue
		}
		c.withFormulas(t, s[:i])
		from := model.UTF16Len(t.Text)
		c.withFormulas(t, inner)
		kind := "marked"
		if delim == "||" {
			kind = "spoiler"
		}
		t.Mark(from, model.Entity{Kind: kind})
		s = s[i+2+end+2:]
	}
}

// nextMark is where the first == or || in s is, and which it is.
func nextMark(s string) (int, string) {
	a, b := strings.Index(s, "=="), strings.Index(s, "||")
	switch {
	case a < 0 && b < 0:
		return -1, ""
	case b < 0 || a >= 0 && a < b:
		return a, "=="
	default:
		return b, "||"
	}
}

// withFormulas adds s to t, the placeholders in it as their formulas: a
// formula's source, marked as math.
func (c *converter) withFormulas(t *model.RichText, s string) {
	start := 0
	for i, r := range s {
		f, ok := c.formula(r)
		if !ok {
			continue
		}
		t.Text += s[start:i]
		from := model.UTF16Len(t.Text)
		t.Text += f.tex
		t.Mark(from, model.Entity{Kind: "math"})
		start = i + utf8.RuneLen(r)
	}
	t.Text += s[start:]
}

// list converts a list: an item of a tight list that is one paragraph is
// text; others hold blocks.
func (c *converter) list(n *cmark.Node) model.RichBlock {
	b := model.RichBlock{Kind: model.RichList, Ordered: n.Ordered}
	if n.Ordered && n.Start != 1 {
		start := n.Start
		b.Start = &start
	}
	for _, item := range n.Children {
		li := model.RichListItem{Checkbox: item.Task != cmark.NoTask, Checked: item.Task == cmark.Done}
		blocks := c.blocks(item.Children)
		if len(blocks) == 1 && blocks[0].Kind == model.RichParagraph {
			li.Text = blocks[0].Text
		} else {
			li.Blocks = blocks
		}
		b.Items = append(b.Items, li)
	}
	return b
}

// table converts a table: its first row is its header, and its columns
// are aligned as the row under that says.
func (c *converter) table(n *cmark.Node) model.RichBlock {
	b := model.RichBlock{Kind: model.RichTable, Bordered: true}
	for _, row := range n.Children {
		var r model.RichTableRow
		for i, cell := range row.Children {
			tc := model.RichTableCell{Text: c.text(cell.Children), Header: row.Header}
			if i < len(n.Alignments) {
				switch n.Alignments[i] {
				case 'c':
					tc.Align = "center"
				case 'r':
					tc.Align = "right"
				}
			}
			r.Cells = append(r.Cells, tc)
		}
		b.Rows = append(b.Rows, r)
	}
	return b
}

// anchor names a heading of text as Telegram Desktop does
// (AnchorIdBaseFromText): its letters and digits in lower case, each run
// of anything else one hyphen between them, "section" when that leaves
// nothing; the second of a name has "-2" after it.
func (c *converter) anchor(text string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			hyphen = false
		} else if b.Len() > 0 {
			hyphen = true
		}
	}
	base := b.String()
	if base == "" {
		base = "section"
	}
	c.anchors[base]++
	if n := c.anchors[base]; n > 1 {
		return base + "-" + strconv.Itoa(n)
	}
	return base
}

// link is where a link to target goes, as Telegram Desktop takes it
// (ClassifiedLink): an anchor of the file; the web, Telegram or mail; or,
// relative, another Markdown file in the file's directory or under it, as
// a file URL. Anything else is no link: "".
func (c *converter) link(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "#") {
		return "#" + model.AnchorName(target[1:])
	}
	if u, err := url.Parse(target); err == nil && u.Scheme != "" {
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "tg", "mailto", "tonsite":
			return target
		}
		return ""
	}
	if c.dir == "" || c.dir == "." || filepath.IsAbs(target) || strings.Contains(target, "?") {
		return ""
	}
	file, fragment, _ := strings.Cut(target, "#")
	if unescaped, err := url.PathUnescape(file); err == nil {
		file = unescaped
	}
	path := filepath.Clean(filepath.Join(c.dir, filepath.FromSlash(file)))
	if rel, err := filepath.Rel(c.dir, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !IsMarkdownName(path) {
		return ""
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if fragment != "" {
		u.Fragment = model.AnchorName(fragment)
	}
	return u.String()
}

// IsMarkdownName reports whether name is the name of a Markdown file.
func IsMarkdownName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return true
	}
	return false
}
