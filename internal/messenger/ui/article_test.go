// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

func richMessage(page model.RichPage) model.Message {
	summary := page.Summary()
	return model.Message{Key: model.MessageKey{ChatID: 100, MessageID: 1}, Text: summary.Text, Entities: summary.Entities, Rich: &page, ContentRevision: 1}
}

func richText(s string) model.RichText { return model.RichText{Text: s} }

// An article's text is its blocks' texts, each after a line break that is
// not drawn; a block that shows nothing is left out, and an article of text
// only is as wide as its text.
func TestPrepareArticle(t *testing.T) {
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichHeading, Level: 2, Text: richText("Заголовок 👋")},
		{Kind: model.RichParagraph},
		{Kind: model.RichParagraph, Text: richText("Абзац")},
		{Kind: model.RichList, Items: []model.RichListItem{{Text: richText("пункт")}, {Checkbox: true, Checked: true, Text: richText("задача")}}},
	}}
	doc := prepareArticle(page, localization.For("ru"), time.Now())
	if len(doc.blocks) != 3 || len(doc.leaves) != 4 || doc.wide {
		t.Fatalf("%d blocks, %d leaves, wide %v", len(doc.blocks), len(doc.leaves), doc.wide)
	}
	var all strings.Builder
	for _, r := range doc.runs {
		all.WriteString(r.Text)
	}
	if got := all.String(); got != "Заголовок 👋\nАбзац\nпункт\nзадача" {
		t.Fatalf("text %q", got)
	}
	for _, leaf := range doc.leaves {
		before := 0
		for _, r := range doc.runs[:leaf.first] {
			before += len([]rune(r.Text))
		}
		if leaf.runeStart != before {
			t.Fatalf("a leaf starts at rune %d, not %d", leaf.runeStart, before)
		}
	}
	list := doc.blocks[2]
	if list.items[0].marker != "•" || !list.items[1].checkbox || !list.items[1].checked {
		t.Fatalf("items %+v", list.items)
	}
	if doc := prepareArticle(model.RichPage{Blocks: []model.RichBlock{{Kind: model.RichDivider}}}, localization.For("ru"), time.Now()); !doc.wide {
		t.Fatal("a divider does not take the width")
	}
}

// Cells spanning rows and columns take their places as in HTML.
func TestTablePlaces(t *testing.T) {
	rows := []articleRow{
		{cells: []articleCell{{colspan: 1, rowspan: 2}, {colspan: 2, rowspan: 1}}},
		{cells: []articleCell{{colspan: 1, rowspan: 1}, {colspan: 1, rowspan: 1}}},
		{cells: []articleCell{{colspan: 3, rowspan: 9}}},
	}
	places := tablePlaces(rows)
	want := [][]tablePlace{
		{{0, 0, 1, 2}, {0, 1, 2, 1}},
		{{1, 1, 1, 1}, {1, 2, 1, 1}},
		{{2, 0, 3, 1}},
	}
	for i := range want {
		for j := range want[i] {
			if places[i][j] != want[i][j] {
				t.Fatalf("cell %d,%d at %+v, want %+v", i, j, places[i][j], want[i][j])
			}
		}
	}
	if got := tableColumns(rows); got != 3 {
		t.Fatalf("%d columns", got)
	}
}

// Text selected across an article's blocks copies as their texts, one to a
// line; the markers of lists are not its text.
func TestArticleSelectsAcrossBlocks(t *testing.T) {
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichHeading, Level: 1, Text: richText("Заголовок")},
		{Kind: model.RichList, Ordered: true, Items: []model.RichListItem{{Text: richText("первый")}, {Text: richText("второй")}}},
		{Kind: model.RichTable, Rows: []model.RichTableRow{
			{Cells: []model.RichTableCell{{Text: richText("ячейка")}, {Text: richText("ещё")}}},
			{Cells: []model.RichTableCell{{Text: richText("ниже"), Colspan: 2}}},
		}},
		{Kind: model.RichParagraph, Text: richText("конец")},
	}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	fragments := h.row.text.fragments
	first, last := fragments[0].Bounds, fragments[len(fragments)-1].Bounds
	h.pointerDrag(f32.Pt(float32(first.Min.X), float32(first.Min.Y+2)), f32.Pt(float32(last.Max.X), float32(last.Max.Y-2)))
	if got := h.row.selectedText(); got != "Заголовок\nпервый\nвторой\nячейка\nещё\nниже\nконец" {
		t.Fatalf("selected %q", got)
	}
	// A table's cells are where their rows are drawn, under the blocks
	// before it, the second row under the first.
	at := func(text string) image.Rectangle {
		for _, f := range h.row.text.fragments {
			if h.row.runs[f.Index].Text == text {
				return f.Bounds
			}
		}
		t.Fatalf("no fragment says %q", text)
		return image.Rectangle{}
	}
	if cell, row2, item := at("ячейка"), at("ниже"), at("второй"); cell.Min.Y <= item.Max.Y || row2.Min.Y <= cell.Max.Y || at("конец").Min.Y <= row2.Max.Y {
		t.Fatalf("cells at %v and %v, after %v", cell, row2, item)
	}
}

// Details open and close by their header, and show their blocks only open.
func TestArticleDetailsToggle(t *testing.T) {
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichDetails, Text: richText("Подробнее"), Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: richText("скрытое")}}},
	}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	shows := func(text string) bool {
		for _, f := range h.row.text.fragments {
			if strings.Contains(h.row.runs[f.Index].Text, text) {
				return true
			}
		}
		return false
	}
	if shows("скрытое") {
		t.Fatal("closed details show their blocks")
	}
	h.clickText("Подробнее")
	h.frame()
	if !shows("скрытое") {
		t.Fatal("opened details hide their blocks")
	}
	h.clickText("Подробнее")
	h.frame()
	if shows("скрытое") {
		t.Fatal("closed again, details show their blocks")
	}
}

// Details unfold: their height grows from the header's to the whole over
// a few frames, and back when they close; opened for an anchor, they open
// at once.
func TestArticleDetailsUnfold(t *testing.T) {
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichDetails, Text: richText("Подробнее"), Blocks: []model.RichBlock{
			{Kind: model.RichParagraph, Text: richText("скрытое")},
			{Kind: model.RichParagraph, Text: richText("ещё строка")},
			{Kind: model.RichParagraph, Text: richText("и ещё одна")},
		}},
	}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	h.animate = true
	h.frame()
	closed := h.row.text.size.Y
	h.clickText("Подробнее")
	h.frame()
	opening := h.row.text.size.Y
	for range 40 {
		h.frame()
	}
	full := h.row.text.size.Y
	if !(closed < opening && opening < full) {
		t.Fatalf("closed %d, opening %d, open %d", closed, opening, full)
	}
	h.clickText("Подробнее")
	h.frame()
	if closing := h.row.text.size.Y; closing <= closed || closing >= full {
		t.Fatalf("closing %d, between %d and %d", closing, closed, full)
	}
	for range 40 {
		h.frame()
	}
	if h.row.text.size.Y != closed {
		t.Fatalf("closed again at %d, not %d", h.row.text.size.Y, closed)
	}
	for _, b := range h.row.article.blocks {
		h.row.articleState.toggled[b.id] = true
		h.row.articleState.instant = map[int]bool{b.id: true}
	}
	h.frame()
	if h.row.text.size.Y != full {
		t.Fatalf("opened for an anchor at %d, not %d", h.row.text.size.Y, full)
	}
}

// A link button and a button in the text ask to open their link; a code
// block of an article copies its text.
func TestArticleButtonsAndCode(t *testing.T) {
	var label model.RichText
	label.Append(richText("Нажмите "))
	from := model.UTF16Len(label.Text)
	label.Append(richText("здесь"))
	label.Mark(from, model.Entity{Kind: "button", Button: &model.MessageButton{Kind: "url", URL: "https://example.com/text"}})
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichButtons, Buttons: []model.RichButton{{Text: richText("Сайт"), Button: model.MessageButton{Kind: "url", URL: "https://example.com/row"}}}},
		{Kind: model.RichParagraph, Text: label},
		{Kind: model.RichCode, Language: "go", Text: richText("fmt.Println(1)")},
	}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	h.click(image.Pt(100, 17))
	if h.page.link != "https://example.com/row" {
		t.Fatalf("the row's button asks to open %q", h.page.link)
	}
	h.page.linkModal.Close()
	h.page.link = ""
	h.frame()
	h.clickText("здесь")
	if h.page.link != "https://example.com/text" {
		t.Fatalf("the text's button asks to open %q", h.page.link)
	}
	code := &h.row.article.leaves[len(h.row.article.leaves)-1]
	code.action.click.Click()
	h.frame()
	if _, copied, ok := h.router.WriteClipboard(); !ok || string(copied) != "fmt.Println(1)" {
		t.Fatalf("copied %q, %v", copied, ok)
	}
}

// pointerDrag presses at from, drags to to and releases there.
func (h *entityHarness) pointerDrag(from, to f32.Point) {
	h.now = h.now.Add(time.Second)
	for _, e := range []pointer.Event{
		{Kind: pointer.Press, Position: from},
		{Kind: pointer.Move, Position: to},
		{Kind: pointer.Release, Position: to},
	} {
		e.Source, e.Buttons, e.Time = pointer.Mouse, pointer.ButtonPrimary, time.Duration(h.now.UnixNano())
		h.router.Queue(e)
		h.frame()
	}
}

// Anchors are kept with the details around them, outermost first: of
// blocks, of list items, and inside their text; the first of a name is
// the one links go to. Drawn, each is where its block or item is.
func TestArticleAnchors(t *testing.T) {
	inline := richText("текст")
	inline.Anchors = []string{"inline"}
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichParagraph, Anchor: "top", Text: inline},
		{Kind: model.RichList, Items: []model.RichListItem{{Text: richText("первый")}, {Anchor: "item", Text: richText("второй")}}},
		{Kind: model.RichDetails, Open: true, Text: richText("Снаружи"), Blocks: []model.RichBlock{
			{Kind: model.RichDetails, Text: richText("Внутри"), Blocks: []model.RichBlock{{Kind: model.RichAnchor, Anchor: "deep"}, {Kind: model.RichParagraph, Anchor: "top", Text: richText("второй top")}}},
		}},
	}}
	doc := prepareArticle(page, localization.For("ru"), time.Now())
	if len(doc.anchors["top"]) != 0 || len(doc.anchors["inline"]) != 0 || len(doc.anchors["item"]) != 0 {
		t.Fatalf("anchors outside details are hidden: %v", doc.anchors)
	}
	if deep := doc.anchors["deep"]; len(deep) != 2 || deep[0] != doc.blocks[2] || deep[1] != doc.blocks[2].children[0] {
		t.Fatalf("the deep anchor is hidden by %v", deep)
	}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	tops := h.row.articleState.tops
	if tops["top"] != 0 || tops["inline"] != 0 || tops["item"] <= 0 {
		t.Fatalf("tops %v", tops)
	}
	if _, ok := tops["deep"]; ok {
		t.Fatal("an anchor in closed details is laid out")
	}
	if !h.row.articleState.openTo(h.row.article, "deep") || h.row.articleState.openTo(h.row.article, "nowhere") {
		t.Fatal("openTo finds the anchors it should not, or not those it should")
	}
	h.frame()
	if deep, item := h.row.articleState.tops["deep"], tops["item"]; deep <= item {
		t.Fatalf("opened, the deep anchor is at %d, over the item at %d", deep, item)
	}
}

// A table too wide for its article keeps each column as wide as its
// longest word and scrolls sideways, by the wheel and by its scrollbar's
// thumb, its text with it.
func TestArticleWideTableScrolls(t *testing.T) {
	var cells []model.RichTableCell
	for i := range 6 {
		cells = append(cells, model.RichTableCell{Text: richText(fmt.Sprintf("Unbreakablelongwordhere%d и ещё", i))})
	}
	page := model.RichPage{Blocks: []model.RichBlock{{Kind: model.RichTable, Rows: []model.RichTableRow{{Cells: cells}, {Cells: cells}}}}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	// at is where the first line of the cell that says text is: its
	// glyphs', which run past the line when a word is too wide for it.
	at := func(text string) image.Rectangle {
		for _, f := range h.row.text.fragments {
			if strings.HasPrefix(h.row.runs[f.Index].Text, text) {
				r := f.Bounds
				for _, c := range f.Clusters {
					r = r.Union(c.Bounds)
				}
				return r
			}
		}
		t.Fatalf("no fragment says %q", text)
		return image.Rectangle{}
	}
	first, last := at("Unbreakablelongwordhere0"), at("Unbreakablelongwordhere5")
	if last.Max.X <= 400 {
		t.Fatalf("the table fits: its last cell ends at %d", last.Max.X)
	}
	if next := at("Unbreakablelongwordhere1"); first.Max.X >= next.Min.X {
		t.Fatalf("the first cell's word, to %d, runs into the next one's, from %d", first.Max.X, next.Min.X)
	}
	if h.row.text.size.Y < first.Max.Y*2+10 {
		t.Fatalf("no scrollbar under the table: the article is %d high", h.row.text.size.Y)
	}
	h.router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(50, float32(first.Min.Y+2)), Scroll: f32.Pt(60, 0)})
	h.frame()
	if moved := at("Unbreakablelongwordhere0"); moved.Min.X != first.Min.X-60 {
		t.Fatalf("scrolled 60 px, the first cell moved from %d to %d", first.Min.X, moved.Min.X)
	}
	// The thumb, under the table at its left, drags the table along.
	before := at("Unbreakablelongwordhere0").Min.X
	y := float32(h.row.text.size.Y - 5)
	x := float32(20 + 60*400/(last.Max.X+8-400))
	h.pointerDrag(f32.Pt(x, y), f32.Pt(x+40, y))
	if after := at("Unbreakablelongwordhere0").Min.X; after >= before {
		t.Fatalf("dragging the thumb moved the first cell from %d to %d", before, after)
	}
}

// Formulas are drawn once RaTeX lays them out: one inline in the line as
// one box of all its source, one of a block in the middle, which selects
// and copies as its source, and one RaTeX cannot read as its source.
func TestArticleDrawsFormulas(t *testing.T) {
	var line model.RichText
	line.Append(richText("Энергия "))
	from := model.UTF16Len(line.Text)
	line.Append(richText("E = mc^2"))
	line.Mark(from, model.Entity{Kind: "math"})
	line.Append(richText(" и всё."))
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichParagraph, Text: line},
		{Kind: model.RichMath, Formula: `\frac{a}{b}`},
		{Kind: model.RichMath, Formula: `\frac{1}{`},
	}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	waitFormulas(t)
	h.frame()
	clustersOf := func(text string) (int, image.Rectangle) {
		for _, f := range h.row.text.fragments {
			if h.row.runs[f.Index].Text == text {
				return len(f.Clusters), f.Bounds
			}
		}
		t.Fatalf("no fragment says %q", text)
		return 0, image.Rectangle{}
	}
	if n, _ := clustersOf("E = mc^2"); n != 1 {
		t.Fatalf("the inline formula is %d clusters", n)
	}
	n, block := clustersOf(`\frac{a}{b}`)
	if n != 1 || block.Dx() >= 380 || block.Min.X < 100 {
		t.Fatalf("the display formula is %d clusters at %v", n, block)
	}
	if n, _ := clustersOf(`\frac{1}{`); n < 2 {
		t.Fatalf("the formula RaTeX cannot read is %d clusters", n)
	}
	h.pointerDrag(f32.Pt(2, 2), f32.Pt(float32(h.row.text.size.X-2), float32(h.row.text.size.Y-2)))
	if got := h.row.selectedText(); !strings.Contains(got, "E = mc^2") || !strings.Contains(got, `\frac{a}{b}`) {
		t.Fatalf("selected %q", got)
	}
}

// In an article, a link is under a hand too, and its plain text is not.
func TestArticleEntityCursors(t *testing.T) {
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichHeading, Level: 2, Text: richText("Заголовок")},
		{Kind: model.RichParagraph, Text: model.RichText{Text: "текст ссылка", Entities: []model.Entity{{Kind: "url", Offset: 6, Length: 6, URL: "https://example.com"}}}},
	}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	over := func(text string) pointer.Cursor {
		t.Helper()
		for _, f := range h.row.text.fragments {
			if h.row.runs[f.Index].Text == text {
				c := f.Bounds.Min.Add(f.Bounds.Size().Div(2))
				h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(c.X), float32(c.Y))})
				h.frame()
				return h.router.Cursor()
			}
		}
		t.Fatalf("no fragment says %q", text)
		return 0
	}
	if got := over("ссылка"); got != pointer.CursorPointer {
		t.Errorf("over the link: %v", got)
	}
	if got := over("Заголовок"); got != pointer.CursorText {
		t.Errorf("over the heading: %v", got)
	}
}

// A photo of an article, which a press opens, is under a hand.
func TestArticleMediaCursor(t *testing.T) {
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "photo", Width: 300, Height: 200}}}},
	}}
	// The photo does not load: the store has none.
	store := &entityStore{}
	h := &entityHarness{t: t, store: store, page: newChatPage(&noMediaStore{store}, func() {}), m: richMessage(page), now: time.Unix(1_790_000_000, 0), l: localization.For("en")}
	h.page.kind, h.page.chat = model.KindUser, 100
	h.row = newMessageRow(h.m, h.l, h.now)
	t.Cleanup(h.page.Close)
	h.frame()
	c := h.row.text.size.Div(2)
	if c.X == 0 || c.Y == 0 {
		t.Fatalf("the article is %v", h.row.text.size)
	}
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(c.X), float32(c.Y))})
	h.frame()
	if got := h.router.Cursor(); got != pointer.CursorPointer {
		t.Fatalf("over the photo: %v", got)
	}
}

// noMediaStore has no media.
type noMediaStore struct{ *entityStore }

func (noMediaStore) Media(context.Context, model.Message) ([]byte, error) {
	return nil, errors.New("no media")
}
