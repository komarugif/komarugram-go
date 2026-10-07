// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// htmlStore gives media's bytes by their IDs, the whole of a message cut
// short, and fails for media it does not have.
type htmlStore struct {
	entityStore
	media map[string][]byte
	whole model.RichPage
}

func (s *htmlStore) Media(_ context.Context, m model.Message) ([]byte, error) {
	if b, ok := s.media[m.Media.ID]; ok {
		return b, nil
	}
	return nil, errors.New("no media")
}

func (s *htmlStore) RichMessage(context.Context, model.MessageKey) (model.RichPage, error) {
	return s.whole, nil
}

// A rich message is saved whole, in a folder named after its title, its
// page under the same name and its media beside it; a second time, in a
// folder of its own; media that cannot be had is told of in the page.
func TestSaveHTML(t *testing.T) {
	photo := &model.MessageMedia{ID: "p1", MIMEType: "image/jpeg"}
	file := &model.MessageMedia{ID: "f1", FileName: "notes: draft?.txt"}
	whole := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichHeading, Level: 1, Text: model.RichText{Text: "Отчёт / 2026"}},
		{Kind: model.RichParagraph, Text: model.RichText{Text: "Formula $x$", Entities: []model.Entity{{Kind: "math", Offset: 8, Length: 3}}}},
		{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessagePhoto, Media: photo}}},
		{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessageFile, Media: file}}},
		{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "gone"}}}},
	}}
	store := &htmlStore{media: map[string][]byte{"p1": []byte("\xff\xd8\xff\xe0 jpeg"), "f1": []byte("text")}, whole: whole}
	p := newChatPage(store, func() {})
	t.Cleanup(p.Close)
	l := localization.For("en")
	m := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 2}, Rich: &model.RichPage{Part: true, Blocks: whole.Blocks[:1]}}
	if !canSaveHTML(m) {
		t.Fatal("a rich message cannot be saved")
	}
	if forbidden := m; true {
		forbidden.NoForwards = true
		if canSaveHTML(forbidden) {
			t.Fatal("a message that may not be forwarded can be saved")
		}
	}
	dir := t.TempDir()
	for i, want := range []string{"Отчёт _ 2026", "Отчёт _ 2026 (2)"} {
		path, err := p.writeHTML(context.Background(), m, whole, dir, l)
		if err != nil {
			t.Fatal(err)
		}
		if path != filepath.Join(dir, want, want+".html") {
			t.Fatalf("save %d: %s", i, path)
		}
		page, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"<title>Отчёт / 2026</title>", `<img src="media/photo_1.jpg"`, `<a class="file" href="media/notes_ draft_.txt" download>`, l.T("rich.html_media_missing"), `<span class="math"><svg`} {
			if !strings.Contains(string(page), s) {
				t.Errorf("no %s in\n%s", s, page)
			}
		}
		if b, err := os.ReadFile(filepath.Join(dir, want, "media", "notes_ draft_.txt")); err != nil || string(b) != "text" {
			t.Fatalf("the file: %q, %v", b, err)
		}
	}
	// Saved in the background, the whole message is asked for and the
	// toast says where it went.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("USERPROFILE", home)
	p.saveHTML(m, l)
	deadline := time.Now().Add(30 * time.Second)
	for p.htmlBusy.Load() || len(p.htmlDone) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the message was not saved")
		}
		time.Sleep(5 * time.Millisecond)
	}
	p.htmlEvents(l)
	saved := filepath.Join(home, "Отчёт _ 2026", "Отчёт _ 2026.html")
	if !strings.Contains(p.toast.Text(), saved) {
		t.Fatalf("toast %q, not of %s", p.toast.Text(), saved)
	}
	if page, _ := os.ReadFile(saved); !strings.Contains(string(page), "media/photo_1.jpg") {
		t.Fatal("the part was saved, not the whole message")
	}
	// A page that cannot be written leaves nothing.
	if _, err := p.writeHTML(context.Background(), m, whole, filepath.Join(saved, "not a folder"), l); err == nil {
		t.Fatal("written under a file")
	}
}

// File names keep what every system takes.
func TestFileNamePart(t *testing.T) {
	for in, want := range map[string]string{"a/b:c": "a_b_c", " .hidden. ": "hidden", "CON": "_CON", "con.txt": "_con.txt", strings.Repeat("я", 70): strings.Repeat("я", 64), "tab\there": "tab here"} {
		if got := fileNamePart(in); got != want {
			t.Errorf("%q: %q, not %q", in, got, want)
		}
	}
}

// Text selected in an article copies as text and as HTML: the blocks it is
// in, the first and the last cut to it.
func TestCopyArticleHTML(t *testing.T) {
	page := model.RichPage{Blocks: []model.RichBlock{
		{Kind: model.RichHeading, Level: 2, Text: model.RichText{Text: "Heading"}},
		{Kind: model.RichParagraph, Text: model.RichText{Text: "first second", Entities: []model.Entity{{Kind: "bold", Offset: 6, Length: 6}}}},
		{Kind: model.RichList, Items: []model.RichListItem{{Text: model.RichText{Text: "item"}}}},
		{Kind: model.RichParagraph, Text: model.RichText{Text: "last words"}},
	}}
	h := newEntityHarness(t, richMessage(page), model.KindUser)
	// "Heading\nfirst second\nitem\nlast words", from "second" to "last".
	h.page.activeText = h.row
	h.row.text.anchor, h.row.text.caret = 14, 30
	if got := h.row.selectedText(); got != "second\nitem\nlast" {
		t.Fatalf("selected %q", got)
	}
	html := string(h.row.selectedHTML(h.l))
	if !strings.Contains(html, "<article>\n<p><strong>second</strong></p>\n<ul>\n<li>item</li>\n</ul>\n<p>last</p>\n</article>") {
		t.Fatalf("html:\n%s", html)
	}
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source()}
	copySelection(gtx, h.row)
	_, text, gotHTML, ok := h.router.WriteClipboardHTML()
	if !ok || string(text) != "second\nitem\nlast" || string(gotHTML) != html {
		t.Fatalf("clipboard %q, %d bytes of HTML", text, len(gotHTML))
	}
	// A message's text alone has no HTML.
	plain := newEntityHarness(t, model.Message{Key: model.MessageKey{ChatID: 100, MessageID: 2}, Text: "plain text", ContentRevision: 1}, model.KindUser)
	plain.row.text.anchor, plain.row.text.caret = 0, 5
	if plain.row.selectedHTML(plain.l) != nil {
		t.Fatal("a plain message's text has HTML")
	}
}
