// SPDX-License-Identifier: Unlicense OR MIT

package markdown

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/cmark"
)

var runtime *cmark.Runtime

func prepare(t *testing.T, src, path string) Document {
	t.Helper()
	if runtime == nil {
		r, err := cmark.NewRuntime(context.Background(), cmark.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		runtime = r
	}
	d, err := Prepare(context.Background(), runtime.Parse, []byte(src), path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// entity is the entity of kind over text in t, if there is one.
func entity(t model.RichText, kind, text string) (model.Entity, bool) {
	units := utf16(t.Text)
	for _, e := range t.Entities {
		if e.Kind == kind && e.Offset+e.Length <= len(units) && string16(units[e.Offset:e.Offset+e.Length]) == text {
			return e, true
		}
	}
	return model.Entity{}, false
}

func utf16(s string) []rune {
	var out []rune
	for _, r := range s {
		out = append(out, r)
		if r >= 0x10000 {
			out = append(out, 0)
		}
	}
	return out
}

func string16(units []rune) string {
	var out []rune
	for _, r := range units {
		if r != 0 {
			out = append(out, r)
		}
	}
	return string(out)
}

// The blocks of GFM become an article's: headings with anchors as
// Telegram Desktop names them, paragraphs with their styles, lists with
// their checkboxes and numbers, tables with their alignments, code with
// its language, quotes and dividers.
func TestPrepareBlocks(t *testing.T) {
	d := prepare(t, "# Привет, мир!\n\n## Привет, мир!\n\n"+
		"Text *it* **bold** ~~gone~~ `code` [web](https://example.com) and <u>under</u>, H<sub>2</sub>O.\n\n"+
		"- [x] done\n- [ ] open\n\n"+
		"3. three\n4. four\n\n"+
		"| a | b |\n|:-:|--:|\n| 1 | 2 |\n\n"+
		"```go\n\tfmt.Println()\n```\n\n> quoted\n\n---\n", "")
	b := d.Page.Blocks
	kinds := []string{}
	for _, x := range b {
		kinds = append(kinds, x.Kind)
	}
	want := []string{model.RichHeading, model.RichHeading, model.RichParagraph, model.RichList, model.RichList, model.RichTable, model.RichCode, model.RichQuote, model.RichDivider}
	if !slices.Equal(kinds, want) {
		t.Fatalf("blocks %v", kinds)
	}
	if d.Title != "Привет, мир!" || b[0].Anchor != "привет-мир" || b[1].Anchor != "привет-мир-2" || b[1].Level != 2 {
		t.Fatalf("title %q, anchors %q %q", d.Title, b[0].Anchor, b[1].Anchor)
	}
	p := b[2].Text
	for _, c := range [][2]string{{"italic", "it"}, {"bold", "bold"}, {"strike", "gone"}, {"code", "code"}, {"url", "web"}, {"underline", "under"}, {"sub", "2"}} {
		if _, ok := entity(p, c[0], c[1]); !ok {
			t.Errorf("no %s over %q in %q %+v", c[0], c[1], p.Text, p.Entities)
		}
	}
	if !b[3].Items[0].Checked || !b[3].Items[1].Checkbox || b[3].Items[1].Checked {
		t.Fatalf("task list %+v", b[3].Items)
	}
	if b[4].Start == nil || *b[4].Start != 3 || !b[4].Ordered {
		t.Fatalf("ordered list %+v", b[4])
	}
	if r := b[5].Rows; len(r) != 2 || !r[0].Cells[0].Header || r[1].Cells[0].Align != "center" || r[1].Cells[1].Align != "right" {
		t.Fatalf("table %+v", r)
	}
	if b[6].Language != "go" || b[6].Text.Text != "    fmt.Println()" {
		t.Fatalf("code %+v", b[6])
	}
}

// Formulas are found as Telegram Desktop finds them: in dollars outside
// code, the markup inside them left alone, an amount of money or prose
// between dollars not; a display formula is a block of its own, and so is
// a ```math block.
func TestPrepareMath(t *testing.T) {
	d := prepare(t, "Inline $a_1 * b_2 = c^2$ here, `$x$` is code, $100$ is money and $two words$ are prose.\n\n"+
		"Before\n$$\n\\frac{a}{b}\n$$\nafter.\n\n```math\nE = mc^2\n```\n", "")
	b := d.Page.Blocks
	p := b[0].Text
	if _, ok := entity(p, "math", "a_1 * b_2 = c^2"); !ok {
		t.Fatalf("inline formula in %q %+v", p.Text, p.Entities)
	}
	if _, ok := entity(p, "code", "$x$"); !ok {
		t.Fatalf("code in %q", p.Text)
	}
	for _, e := range p.Entities {
		if e.Kind == "italic" || e.Kind == "math" && e.Length < 10 {
			t.Fatalf("%+v in %q", e, p.Text)
		}
	}
	kinds := []string{}
	for _, x := range b {
		kinds = append(kinds, x.Kind)
	}
	if !slices.Equal(kinds, []string{model.RichParagraph, model.RichParagraph, model.RichMath, model.RichParagraph, model.RichMath}) {
		t.Fatalf("blocks %v", kinds)
	}
	if b[2].Formula != `\frac{a}{b}` || b[4].Formula != "E = mc^2" {
		t.Fatalf("formulas %q %q", b[2].Formula, b[4].Formula)
	}
}

// Marks, spoilers, details, footnotes and links: footnotes are numbered
// as they are referred to, listed at the end with their anchors and links
// back; links go to anchors, the web, or Markdown files beside the file.
func TestPrepareTelegram(t *testing.T) {
	dir := t.TempDir()
	d := prepare(t, "A ==marked== and ||hidden|| word.[^b] Again[^a].\n\n"+
		"<details open>\n<summary>More</summary>\n\nInside.\n\n</details>\n\n"+
		"[anchor](#Section) [next](other.md#Top) [up](../secret.md) [script](javascript:alert(1)) [txt](notes.txt)\n\n"+
		"[^a]: First defined.\n[^b]: Referred to first.\n", filepath.Join(dir, "doc.md"))
	b := d.Page.Blocks
	p := b[0].Text
	if _, ok := entity(p, "marked", "marked"); !ok {
		t.Fatalf("mark in %q", p.Text)
	}
	if _, ok := entity(p, "spoiler", "hidden"); !ok {
		t.Fatalf("spoiler in %q", p.Text)
	}
	if e, ok := entity(p, "url", "1"); !ok || e.URL != "#fn-1" || !slices.Contains(p.Anchors, "fnref-1") {
		t.Fatalf("first reference in %q %+v %v", p.Text, p.Entities, p.Anchors)
	}
	if b[1].Kind != model.RichDetails || !b[1].Open || b[1].Text.Text != "More" || len(b[1].Blocks) != 1 {
		t.Fatalf("details %+v", b[1])
	}
	links := b[2].Text
	urls := map[string]string{}
	for _, name := range []string{"anchor", "next", "up", "script", "txt"} {
		e, _ := entity(links, "url", name)
		urls[name] = e.URL
	}
	if urls["anchor"] != "#section" || urls["next"] != "file://"+filepath.ToSlash(filepath.Join(dir, "other.md"))+"#top" || urls["up"] != "" || urls["script"] != "" || urls["txt"] != "" {
		t.Fatalf("links %v", urls)
	}
	last := b[len(b)-1]
	if last.Kind != model.RichList || len(last.Items) != 2 || last.Items[0].Anchor != "fn-1" || last.Items[0].Text.Text != "Referred to first. ↩" {
		t.Fatalf("footnotes %+v", last)
	}
}

// What is not Markdown is text, and a file of nothing is an error.
func TestPrepareOdd(t *testing.T) {
	d := prepare(t, "\uFEFFline one\r\nline two \U000F0001\n\n<!-- comment -->\n\n![a cat](https://example.com/cat.png)\n", "")
	if b := d.Page.Blocks; len(b) != 2 || b[0].Text.Text != "line one line two �" {
		t.Fatalf("blocks %+v", b)
	}
	if e, ok := entity(d.Page.Blocks[1].Text, "url", "a cat"); !ok || e.URL != "https://example.com/cat.png" {
		t.Fatalf("image %+v", d.Page.Blocks[1].Text)
	}
	if _, err := Prepare(context.Background(), runtime.Parse, []byte("\n\n<!-- -->\n"), ""); err != ErrEmpty {
		t.Fatalf("an empty file: %v", err)
	}
}
