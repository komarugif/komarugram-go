// SPDX-License-Identifier: Unlicense OR MIT

// Package richhtml writes a rich message as an HTML page, as Telegram
// Desktop's "Save as HTML" does (iv/iv_rich_message_html_export.cpp): its
// blocks as the elements HTML has for them, its media from files beside
// the page, its formulas as SVG, in a style of our own. The same page, of
// some of the blocks, is what copying them puts on the clipboard as HTML.
//
// Nothing of the message's runs as code: links go only to the web,
// Telegram, mail and phone numbers and to the page's own anchors, and the
// page has no script.
package richhtml

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"komarugram/internal/messenger/model"
)

// Options are what a page needs besides its message.
type Options struct {
	// Title is the page's title.
	Title string
	// Lang is the language of the page's texts, as "ru".
	Lang string
	// Media is where the page finds the media of the message, by their
	// IDs: a path relative to the page, or a data: URI. Media not in it
	// show Missing.
	Media map[string]string
	// Formula is a formula drawn, its source and whether it is a display
	// one: markup to put in the page, as SVG; "" shows its source.
	Formula func(tex string, display bool) string
	// Date writes a formatted date's entity; nil leaves its text.
	Date func(model.Entity) string
	// Day writes the date of an author's line, a post or an article.
	Day func(time.Time) string
	// Missing is what shows in place of media that is not there, and
	// Embed names embedded content, which the page links to.
	Missing, Embed string
	// HideSpoilers leaves the text of spoilers out, as copying text leaves
	// out those not revealed.
	HideSpoilers bool
	// SkipMissing leaves media that is not there out, in place of Missing:
	// for the clipboard, which takes no files.
	SkipMissing bool
}

// Page is page as an HTML document.
func Page(page model.RichPage, o Options) []byte {
	w := &writer{o: o}
	w.blocks(page.Blocks)
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html")
	if o.Lang != "" {
		fmt.Fprintf(&b, ` lang="%s"`, attr(o.Lang))
	}
	if page.RTL {
		b.WriteString(` dir="rtl"`)
	}
	b.WriteString(">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<meta name=\"generator\" content=\"KomaruGram\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(o.Title))
	b.WriteString("<style>\n" + style + "</style>\n</head>\n<body>\n<article>\n")
	b.WriteString(w.out.String())
	b.WriteString("</article>\n</body>\n</html>\n")
	return []byte(b.String())
}

// Title is what a page is called: its first heading's text, or its first
// text's first line, cut to a length a file's name takes.
func Title(page model.RichPage) string {
	var first, heading string
	var walk func([]model.RichBlock)
	walk = func(blocks []model.RichBlock) {
		for _, b := range blocks {
			if heading != "" {
				return
			}
			text := strings.TrimSpace(b.Text.Text)
			if b.Kind == model.RichHeading && text != "" {
				heading = text
				return
			}
			if first == "" && text != "" && (b.Kind == model.RichParagraph || b.Kind == model.RichQuote) {
				first = text
			}
			walk(b.Blocks)
		}
	}
	walk(page.Blocks)
	title := heading
	if title == "" {
		title = first
	}
	title, _, _ = strings.Cut(title, "\n")
	if utf8.RuneCountInString(title) > 64 {
		title = strings.TrimSpace(string([]rune(title)[:64])) + "…"
	}
	return title
}

// Media are the media of page, each once, in the order they show.
func Media(page model.RichPage) []model.RichMedia {
	var out []model.RichMedia
	seen := map[string]bool{}
	var walk func([]model.RichBlock)
	walk = func(blocks []model.RichBlock) {
		for _, b := range blocks {
			for _, m := range b.Media {
				if m.Media != nil && m.Media.ID != "" && !seen[m.Media.ID] {
					seen[m.Media.ID] = true
					out = append(out, m)
				}
			}
			walk(b.Blocks)
			for _, item := range b.Items {
				walk(item.Blocks)
			}
		}
	}
	walk(page.Blocks)
	return out
}

// writer writes the blocks of a page.
type writer struct {
	o   Options
	out strings.Builder
}

func (w *writer) printf(format string, args ...any) {
	fmt.Fprintf(&w.out, format, args...)
}

func (w *writer) blocks(blocks []model.RichBlock) {
	for _, b := range blocks {
		w.block(b)
	}
}

// id is the id attribute of an anchor, or nothing for none.
func id(anchor string) string {
	if name := model.AnchorName(anchor); name != "" {
		return fmt.Sprintf(` id="%s"`, attr(name))
	}
	return ""
}

func (w *writer) block(b model.RichBlock) {
	switch b.Kind {
	case model.RichHeading:
		level := min(max(b.Level, 1), 6)
		w.printf("<h%d%s>", level, id(b.Anchor))
		w.text(b.Text)
		w.printf("</h%d>\n", level)
	case model.RichParagraph:
		w.printf("<p%s>", id(b.Anchor))
		w.text(b.Text)
		w.printf("</p>\n")
	case model.RichFooter:
		w.printf("<p class=\"footer\"%s>", id(b.Anchor))
		w.text(b.Text)
		w.printf("</p>\n")
	case model.RichThinking:
		w.printf("<blockquote class=\"thinking\"%s>", id(b.Anchor))
		w.text(b.Text)
		w.printf("</blockquote>\n")
	case model.RichAuthorDate:
		w.printf("<p class=\"byline\"%s>", id(b.Anchor))
		w.text(b.Text)
		if !b.Date.IsZero() {
			if b.Text.Text != "" {
				w.printf(" · ")
			}
			w.printf("<time datetime=\"%s\">%s</time>", b.Date.UTC().Format(time.RFC3339), html.EscapeString(w.day(b.Date)))
		}
		w.printf("</p>\n")
	case model.RichCode:
		w.printf("<pre class=\"code\"%s><code", id(b.Anchor))
		if b.Language != "" {
			w.printf(` class="language-%s" data-language="%s"`, attr(strings.ReplaceAll(b.Language, " ", "-")), attr(b.Language))
		}
		w.printf(">%s</code></pre>\n", html.EscapeString(b.Text.Text))
	case model.RichDivider:
		w.printf("<hr%s>\n", id(b.Anchor))
	case model.RichAnchor:
		if b.Anchor != "" {
			w.printf("<a%s></a>\n", id(b.Anchor))
		}
	case model.RichButtons:
		w.buttons(b)
	case model.RichList:
		w.list(b)
	case model.RichQuote:
		class := ""
		if b.Pullquote {
			class = ` class="pullquote"`
		}
		w.printf("<blockquote%s%s>", class, id(b.Anchor))
		if len(b.Blocks) > 0 {
			w.printf("\n")
			w.blocks(b.Blocks)
		} else {
			w.text(b.Text)
		}
		if b.Caption.Text != "" {
			w.printf("<cite>")
			w.text(b.Caption)
			w.printf("</cite>")
		}
		w.printf("</blockquote>\n")
	case model.RichMediaBlock:
		w.media(b)
	case model.RichEmbed:
		w.printf("<figure class=\"embed\"%s>", id(b.Anchor))
		if href := safeURL(b.URL); href != "" {
			w.printf("<a href=\"%s\">%s</a>", attr(href), html.EscapeString(w.o.Embed))
		} else {
			w.printf("<div class=\"missing\">%s</div>", html.EscapeString(w.o.Embed))
		}
		w.caption(b.Caption)
		w.printf("</figure>\n")
	case model.RichEmbedPost:
		w.printf("<blockquote class=\"post\"%s>", id(b.Anchor))
		if b.Author != "" || !b.Date.IsZero() {
			w.printf("<div class=\"post-header\">")
			author := html.EscapeString(b.Author)
			if href := safeURL(b.URL); href != "" && author != "" {
				author = fmt.Sprintf("<a href=\"%s\">%s</a>", attr(href), author)
			}
			w.printf("<b>%s</b>", author)
			if !b.Date.IsZero() {
				w.printf(" <time datetime=\"%s\">%s</time>", b.Date.UTC().Format(time.RFC3339), html.EscapeString(w.day(b.Date)))
			}
			w.printf("</div>\n")
		}
		w.blocks(b.Blocks)
		if b.Caption.Text != "" {
			w.printf("<cite>")
			w.text(b.Caption)
			w.printf("</cite>")
		}
		w.printf("</blockquote>\n")
	case model.RichChannel:
		w.printf("<p class=\"channel\"%s>", id(b.Anchor))
		name := html.EscapeString(b.Title)
		if b.Username != "" {
			w.printf("<a href=\"https://t.me/%s\">%s</a>", attr(url.PathEscape(b.Username)), name)
		} else {
			w.printf("%s", name)
		}
		w.printf("</p>\n")
	case model.RichMath:
		w.printf("<div class=\"math\"%s>", id(b.Anchor))
		if svg := w.formula(b.Formula, true); svg != "" {
			w.printf("%s", svg)
		} else {
			w.printf("<code>%s</code>", html.EscapeString(b.Formula))
		}
		w.printf("</div>\n")
	case model.RichTable:
		w.table(b)
	case model.RichDetails:
		open := ""
		if b.Open {
			open = " open"
		}
		w.printf("<details%s%s><summary>", open, id(b.Anchor))
		w.text(b.Text)
		w.printf("</summary>\n")
		w.blocks(b.Blocks)
		w.printf("</details>\n")
	case model.RichRelated:
		w.related(b)
	case model.RichMap:
		w.printf("<figure class=\"map\"%s>", id(b.Anchor))
		coordinates := fmt.Sprintf("%.6f, %.6f", b.Latitude, b.Longitude)
		zoom := min(max(b.Zoom, 1), 19)
		if zoom == 1 && b.Zoom == 0 {
			zoom = 15
		}
		w.printf("<a href=\"https://www.openstreetmap.org/?mlat=%.6f&amp;mlon=%.6f#map=%d/%.6f/%.6f\">%s</a>", b.Latitude, b.Longitude, zoom, b.Latitude, b.Longitude, html.EscapeString(coordinates))
		w.caption(b.Caption)
		w.printf("</figure>\n")
	}
}

func (w *writer) day(t time.Time) string {
	if w.o.Day != nil {
		return w.o.Day(t)
	}
	return t.Local().Format("2006-01-02")
}

func (w *writer) formula(tex string, display bool) string {
	if w.o.Formula == nil || strings.TrimSpace(tex) == "" {
		return ""
	}
	return w.o.Formula(tex, display)
}

func (w *writer) caption(t model.RichText) {
	if t.Text == "" {
		return
	}
	w.printf("<figcaption>")
	w.text(t)
	w.printf("</figcaption>")
}

func (w *writer) buttons(b model.RichBlock) {
	align := ""
	switch b.Align {
	case "left", "center", "right":
		align = " " + b.Align
	}
	w.printf("<div class=\"buttons%s\"%s>", align, id(b.Anchor))
	for _, button := range b.Buttons {
		class := "button"
		switch button.Style {
		case "primary", "danger", "success", "link":
			class += " " + button.Style
		}
		if href := safeURL(button.Button.URL); href != "" {
			w.printf("<a class=\"%s\" href=\"%s\">", class, attr(href))
			w.text(button.Text)
			w.printf("</a>")
		} else {
			w.printf("<span class=\"%s\">", class)
			w.text(button.Text)
			w.printf("</span>")
		}
	}
	w.printf("</div>\n")
}

// listTypes are the types HTML's lists take as attributes; others, CSS
// names, go to their style.
var listTypes = map[string]bool{"1": true, "a": true, "A": true, "i": true, "I": true}

// listType is the attribute or style of a list's or an item's type.
func listType(t string) string {
	switch {
	case t == "":
		return ""
	case listTypes[t]:
		return fmt.Sprintf(` type="%s"`, t)
	case strings.Trim(t, "abcdefghijklmnopqrstuvwxyz-") == "":
		return fmt.Sprintf(` style="list-style-type:%s"`, t)
	}
	return ""
}

func (w *writer) list(b model.RichBlock) {
	tag := "ul"
	attrs := id(b.Anchor)
	if b.Ordered {
		tag = "ol"
		if b.Start != nil {
			attrs += fmt.Sprintf(` start="%d"`, *b.Start)
		}
		if b.Reversed {
			attrs += " reversed"
		}
		attrs += listType(b.Type)
	}
	w.printf("<%s%s>\n", tag, attrs)
	for _, item := range b.Items {
		attrs := id(item.Anchor)
		if item.Value != nil {
			attrs += fmt.Sprintf(` value="%d"`, *item.Value)
		} else if item.Num != "" && b.Ordered {
			if _, err := strconv.Atoi(item.Num); err != nil {
				// A number written as itself, as "1.2".
				attrs += fmt.Sprintf(` style="list-style-type:%s"`, attr(cssString(item.Num+" ")))
			}
		}
		attrs += listType(item.Type)
		if item.Checkbox {
			// Its box is its marker.
			attrs += ` class="task"`
		}
		w.printf("<li%s>", attrs)
		if item.Checkbox {
			checked := ""
			if item.Checked {
				checked = " checked"
			}
			w.printf("<input type=\"checkbox\" disabled%s> ", checked)
		}
		w.text(item.Text)
		if len(item.Blocks) > 0 {
			w.printf("\n")
			w.blocks(item.Blocks)
		}
		w.printf("</li>\n")
	}
	w.printf("</%s>\n", tag)
}

// cssString is s as a CSS string.
func cssString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\%x ", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (w *writer) table(b model.RichBlock) {
	w.printf("<figure class=\"table\"%s>", id(b.Anchor))
	if b.Text.Text != "" {
		w.printf("<figcaption>")
		w.text(b.Text)
		w.printf("</figcaption>")
	}
	var classes []string
	for _, c := range []struct {
		on   bool
		name string
	}{{b.Bordered, "bordered"}, {b.Striped, "striped"}, {b.Compact, "compact"}} {
		if c.on {
			classes = append(classes, c.name)
		}
	}
	if len(classes) > 0 {
		w.printf("<table class=\"%s\">\n", strings.Join(classes, " "))
	} else {
		w.printf("<table>\n")
	}
	for _, row := range b.Rows {
		w.printf("<tr>")
		for _, cell := range row.Cells {
			tag := "td"
			if cell.Header {
				tag = "th"
			}
			attrs := ""
			if cell.Colspan > 1 {
				attrs += fmt.Sprintf(` colspan="%d"`, cell.Colspan)
			}
			if cell.Rowspan > 1 {
				attrs += fmt.Sprintf(` rowspan="%d"`, cell.Rowspan)
			}
			var styles []string
			switch cell.Align {
			case "center", "right":
				styles = append(styles, "text-align:"+cell.Align)
			}
			switch cell.VAlign {
			case "middle", "bottom":
				styles = append(styles, "vertical-align:"+cell.VAlign)
			}
			if len(styles) > 0 {
				attrs += fmt.Sprintf(` style="%s"`, strings.Join(styles, ";"))
			}
			w.printf("<%s%s>", tag, attrs)
			w.text(cell.Text)
			w.printf("</%s>", tag)
		}
		w.printf("</tr>\n")
	}
	w.printf("</table></figure>\n")
}

func (w *writer) related(b model.RichBlock) {
	w.printf("<section class=\"related\"%s>", id(b.Anchor))
	if b.Text.Text != "" {
		w.printf("<h4>")
		w.text(b.Text)
		w.printf("</h4>")
	}
	for _, a := range b.Related {
		href := safeURL(a.URL)
		if href != "" {
			w.printf("<a class=\"card\" href=\"%s\">", attr(href))
		} else {
			w.printf("<div class=\"card\">")
		}
		if a.Title != "" {
			w.printf("<b>%s</b>", html.EscapeString(a.Title))
		}
		if a.Description != "" {
			w.printf("<span>%s</span>", html.EscapeString(a.Description))
		}
		var meta []string
		if a.Author != "" {
			meta = append(meta, a.Author)
		}
		if !a.Date.IsZero() {
			meta = append(meta, w.day(a.Date))
		}
		if len(meta) > 0 {
			w.printf("<small>%s</small>", html.EscapeString(strings.Join(meta, " · ")))
		}
		if href != "" {
			w.printf("</a>")
		} else {
			w.printf("</div>")
		}
	}
	w.printf("</section>\n")
}

func (w *writer) media(b model.RichBlock) {
	var items []model.RichMedia
	for _, m := range b.Media {
		if m.Media != nil && (!w.o.SkipMissing || w.o.Media[m.Media.ID] != "") {
			items = append(items, m)
		}
	}
	if len(items) == 0 && w.o.SkipMissing {
		if b.Caption.Text != "" {
			w.printf("<p class=\"footer\">")
			w.text(b.Caption)
			w.printf("</p>\n")
		}
		return
	}
	class := "media"
	switch {
	case len(items) > 1 && b.Slideshow:
		class = "media slideshow"
	case len(items) > 1:
		class = "media collage"
	}
	w.printf("<figure class=\"%s\"%s>", class, id(b.Anchor))
	if len(items) == 0 {
		w.printf("<div class=\"missing\">%s</div>", html.EscapeString(w.o.Missing))
	}
	if len(items) > 1 {
		w.printf("<div class=\"items\">")
	}
	for _, m := range items {
		w.mediaItem(m)
	}
	if len(items) > 1 {
		w.printf("</div>")
	}
	w.caption(b.Caption)
	w.printf("</figure>\n")
}

func (w *writer) mediaItem(m model.RichMedia) {
	src, ok := w.o.Media[m.Media.ID]
	if (!ok || src == "") && w.o.SkipMissing {
		return
	}
	if !ok || src == "" {
		w.printf("<div class=\"missing\">%s</div>", html.EscapeString(w.o.Missing))
		return
	}
	src = attr(src)
	size := ""
	if m.Media.Width > 0 && m.Media.Height > 0 {
		size = fmt.Sprintf(` width="%d" height="%d"`, m.Media.Width, m.Media.Height)
	}
	if m.Spoiler {
		// Hidden until it is pressed, which focuses it, as a spoiler is
		// in the client.
		w.printf("<span class=\"spoiler-media\" tabindex=\"0\">")
		defer w.printf("</span>")
	}
	switch m.Kind {
	case model.MessagePhoto, model.MessageSticker:
		w.printf("<img src=\"%s\"%s loading=\"lazy\" alt=\"\">", src, size)
	case model.MessageVideo, model.MessageGIF:
		flags := " controls"
		if m.Kind == model.MessageGIF || m.Autoplay {
			flags = " autoplay muted playsinline"
		}
		if m.Kind == model.MessageGIF || m.Loop {
			flags += " loop"
		}
		w.printf("<video src=\"%s\"%s%s preload=\"metadata\"></video>", src, size, flags)
	case model.MessageMusic, model.MessageVoice:
		w.printf("<div class=\"audio\">")
		if name := strings.TrimSpace(strings.Trim(m.Media.Performer+" — "+m.Media.Title, " —")); name != "" {
			w.printf("<div>%s</div>", html.EscapeString(name))
		}
		w.printf("<audio controls src=\"%s\" preload=\"metadata\"></audio></div>", src)
	default:
		name := m.Media.FileName
		if name == "" {
			name = m.Media.Title
		}
		w.printf("<a class=\"file\" href=\"%s\" download>%s</a>", src, html.EscapeString(name))
	}
}

// safeURL is u if a page may link to it: the web, Telegram, mail, a phone
// number or an anchor of the page, which it names as the page does; ""
// otherwise.
func safeURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "#") {
		if name := model.AnchorName(u); name != "" {
			return "#" + name
		}
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "tg", "mailto", "tel", "tonsite":
		return u
	}
	return ""
}

// attr is s escaped for an attribute's value in double quotes.
func attr(s string) string {
	return html.EscapeString(s)
}
