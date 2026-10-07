// SPDX-License-Identifier: Unlicense OR MIT

package richhtml

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

var tagPattern = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)[^>]*?(/?)>`)

// voids are the elements HTML closes on its own.
var voids = map[string]bool{"br": true, "hr": true, "img": true, "input": true, "meta": true, "rect": true, "path": true}

// balanced checks that every element of page is closed, in order, outside
// <style>.
func balanced(t *testing.T, page string) {
	t.Helper()
	if i, j := strings.Index(page, "<style>"), strings.Index(page, "</style>"); i >= 0 && j > i {
		page = page[:i] + page[j+len("</style>"):]
	}
	page = strings.TrimPrefix(page, "<!DOCTYPE html>")
	var stack []string
	for _, m := range tagPattern.FindAllStringSubmatch(page, -1) {
		closing, name, self := m[1] == "/", strings.ToLower(m[2]), m[3] == "/"
		switch {
		case self || (voids[name] && !closing):
		case closing:
			if len(stack) == 0 || stack[len(stack)-1] != name {
				t.Fatalf("</%s> closes %v", name, stack)
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, name)
		}
	}
	if len(stack) > 0 {
		t.Fatalf("not closed: %v", stack)
	}
}

func text(s string) model.RichText { return model.RichText{Text: s} }

// Each kind of block is the element HTML has for it, with its anchor as
// its id, and the page is well formed.
func TestPageBlocks(t *testing.T) {
	start := 3
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichHeading, Level: 2, Anchor: "Intro", Text: text("Intro")},
		{Kind: model.RichParagraph, Text: model.RichText{Text: "bold link x2 H2O", Entities: []model.Entity{
			{Kind: "bold", Offset: 0, Length: 4}, {Kind: "url", Offset: 5, Length: 4, URL: "https://example.com/?a=1&b=2"},
			{Kind: "sup", Offset: 11, Length: 1}, {Kind: "sub", Offset: 14, Length: 1},
		}}},
		{Kind: model.RichCode, Language: "go", Text: text("if a < b {\n\tx()\n}")},
		{Kind: model.RichList, Ordered: true, Start: &start, Type: "i", Items: []model.RichListItem{
			{Text: text("three"), Anchor: "item"}, {Checkbox: true, Checked: true, Text: text("done")},
		}},
		{Kind: model.RichQuote, Text: text("quoted"), Caption: text("someone")},
		{Kind: model.RichTable, Bordered: true, Text: text("Table"), Rows: []model.RichTableRow{
			{Cells: []model.RichTableCell{{Header: true, Text: text("a")}, {Header: true, Text: text("b"), Align: "right"}}},
			{Cells: []model.RichTableCell{{Colspan: 2, VAlign: "middle", Text: text("ab")}}},
		}},
		{Kind: model.RichDetails, Open: true, Text: text("More"), Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: text("inside")}}},
		{Kind: model.RichMath, Formula: `\frac{a}{b}`},
		{Kind: model.RichDivider},
		{Kind: model.RichMediaBlock, Media: []model.RichMedia{
			{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "p1", Width: 800, Height: 600}},
			{Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "v1"}},
		}, Caption: text("two")},
		{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessageMusic, Media: &model.MessageMedia{ID: "a1", Performer: "Band", Title: "Song"}}}},
		{Kind: model.RichAuthorDate, Text: text("Author"), Date: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)},
		{Kind: model.RichMap, Latitude: 55.75, Longitude: 37.62, Zoom: 12},
		{Kind: model.RichButtons, Buttons: []model.RichButton{{Text: text("Open"), Button: model.MessageButton{URL: "https://example.com"}}, {Text: text("Press"), Style: "primary"}}},
		{Kind: model.RichEmbed, URL: "https://example.com/embed"},
	}}
	out := string(Page(page, Options{Title: "T", Lang: "ru", Media: map[string]string{"p1": "media/p 1.jpg", "a1": "media/a1.mp3"}, Missing: "Media unavailable", Embed: "Embedded content",
		Formula: func(tex string, display bool) string { return "<svg>" + tex + "</svg>" },
		Day:     func(time.Time) string { return "7 Oct" },
	}))
	balanced(t, out)
	for _, want := range []string{
		`<html lang="ru">`, "<title>T</title>",
		`<h2 id="intro">Intro</h2>`,
		`<strong>bold</strong>`, `<a href="https://example.com/?a=1&amp;b=2">link</a>`, `<sup>2</sup>`, `<sub>2</sub>`,
		`<pre class="code"><code class="language-go" data-language="go">if a &lt; b {` + "\n\tx()\n}</code></pre>",
		`<ol start="3" type="i">`, `<li id="item">three</li>`, `<li class="task"><input type="checkbox" disabled checked> done`,
		`<blockquote>quoted<cite>someone</cite></blockquote>`,
		`<table class="bordered">`, `<figcaption>Table</figcaption>`, `<th style="text-align:right">b</th>`, `<td colspan="2" style="vertical-align:middle">ab</td>`,
		"<details open><summary>More</summary>\n<p>inside</p>\n</details>",
		`<div class="math"><svg>\frac{a}{b}</svg></div>`, "<hr>",
		`<figure class="media collage"><div class="items"><img src="media/p 1.jpg" width="800" height="600" loading="lazy" alt=""><div class="missing">Media unavailable</div></div><figcaption>two</figcaption></figure>`,
		`<div>Band — Song</div><audio controls src="media/a1.mp3"`,
		`<p class="byline">Author · <time datetime="2026-10-07T12:00:00Z">7 Oct</time></p>`,
		`href="https://www.openstreetmap.org/?mlat=55.750000&amp;mlon=37.620000#map=12/55.750000/37.620000"`,
		`<a class="button" href="https://example.com">Open</a><span class="button primary">Press</span>`,
		`<figure class="embed"><a href="https://example.com/embed">Embedded content</a></figure>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
}

// What the message holds is text, never markup or script: its letters are
// escaped, links go only where a page may go, and anchors' names cannot
// leave their attribute.
func TestPageEscapes(t *testing.T) {
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichParagraph, Anchor: `x" onclick="alert(1)`, Text: model.RichText{Text: `<script>alert(1)</script> a b c d`, Entities: []model.Entity{
			{Kind: "url", Offset: 26, Length: 1, URL: "javascript:alert(1)"},
			{Kind: "url", Offset: 28, Length: 1, URL: `#Sec"tion`},
			{Kind: "url", Offset: 30, Length: 1, URL: "file:///etc/passwd"},
			{Kind: "url", Offset: 32, Length: 1, URL: "tg://resolve?domain=a"},
		}}},
		{Kind: model.RichCode, Language: `go"><script>`, Text: text("</code></pre><script>")},
		{Kind: model.RichList, Ordered: true, Type: `disc;background:url(x)`, Items: []model.RichListItem{{Num: `1"2`, Text: text("x")}}},
	}}
	out := string(Page(page, Options{Title: "</title><script>"}))
	balanced(t, out)
	if strings.Contains(out, "<script>") || strings.Contains(out, "javascript:") || strings.Contains(out, "file:///") || strings.Contains(out, `onclick="`) || strings.Contains(out, "background:url") {
		t.Fatalf("unsafe:\n%s", out)
	}
	for _, want := range []string{`&lt;script&gt;alert(1)&lt;/script&gt;`, `<a href="#sec&#34;tion">b</a>`, `<a href="tg://resolve?domain=a">d</a>`, `id="x&#34; onclick=&#34;alert(1)"`, `list-style-type:&#34;1\&#34;2 &#34;`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
}

// An anchor inside a text is where it is in it, an entity across it closed
// before and opened after; a spoiler hidden is left out.
func TestPageText(t *testing.T) {
	p := model.RichText{Text: "one two three", Entities: []model.Entity{{Kind: "italic", Offset: 0, Length: 7}, {Kind: "spoiler", Offset: 8, Length: 5}}}
	p.AddAnchor("end")
	p.Anchors, p.AnchorAt = append(p.Anchors, "Mid"), append(p.AnchorAt, 4)
	page := model.RichPage{Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: p}}}
	out := string(Page(page, Options{}))
	if !strings.Contains(out, `<p><em>one </em><a id="mid"></a><em>two</em> <span class="spoiler" tabindex="0">three</span><a id="end"></a></p>`) {
		t.Fatalf("text:\n%s", out)
	}
	if out := string(Page(page, Options{HideSpoilers: true})); strings.Contains(out, "three") || !strings.Contains(out, "[•••]") {
		t.Fatalf("clipboard:\n%s", out)
	}
}

// The demo's article is a whole page, its title its heading, its media
// each once.
func TestPageDemo(t *testing.T) {
	page := mockstore.RichExample(false)
	out := string(Page(page, Options{Title: Title(page)}))
	balanced(t, out)
	if Title(page) == "" {
		t.Fatal("no title")
	}
	seen := map[string]bool{}
	for _, m := range Media(page) {
		if seen[m.Media.ID] {
			t.Fatalf("%s twice", m.Media.ID)
		}
		seen[m.Media.ID] = true
	}
	if len(seen) == 0 {
		t.Fatal("no media")
	}
}
