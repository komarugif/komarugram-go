// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// markdownStore gives a file's bytes, or an error.
type markdownStore struct {
	entityStore
	data []byte
	err  error
}

func (s *markdownStore) Media(context.Context, model.Message) ([]byte, error) { return s.data, s.err }

// A Markdown file opens as an article in a window named after it; one that
// cannot be read is told of and goes to the system's program.
func TestMarkdownFileOpens(t *testing.T) {
	file := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: 7}, Kind: model.MessageFile, Media: &model.MessageMedia{FileName: "README.md", Size: 40}}
	if !markdownFile(file) || markdownFile(model.Message{Media: &model.MessageMedia{FileName: "a.txt"}}) {
		t.Fatal("Markdown files are not told apart")
	}
	store := &markdownStore{data: []byte("# Заголовок\n\nТекст с $x^2$.\n")}
	p := newChatPage(store, func() {})
	t.Cleanup(p.Close)
	var opened, of model.Message
	var title string
	p.openSourceWindow = func(article model.Message, name string, src articleSource) {
		opened, of, title = article, *src.file, name
	}
	l := localization.For("en")
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for p.sourceBusy.Load() || len(p.sourceDone) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the file was not prepared")
			}
			time.Sleep(5 * time.Millisecond)
		}
		p.sourceEvents(l)
	}
	p.openMarkdown(file)
	wait()
	if opened.Rich == nil || len(opened.Rich.Blocks) != 2 || opened.Rich.Blocks[0].Kind != model.RichHeading || of.Key != file.Key || title != "README.md" || opened.Key != file.Key {
		t.Fatalf("opened %+v of %+v as %q", opened.Rich, of.Key, title)
	}
	// The system's program would open the file: it is busy here.
	p.files = &attachmentFiles{busy: true, cancel: func() {}}
	store.err = errors.New("gone")
	opened = model.Message{}
	p.openMarkdown(file)
	wait()
	if opened.Rich != nil || p.toast.Text() != l.T("rich.markdown_cant") {
		t.Fatal("a file that cannot be read opened, or nothing was told")
	}
}

// ivStore has an Instant View, or fails.
type ivStore struct {
	entityStore
	err error
}

func (s *ivStore) InstantView(context.Context, string) (model.RichPage, error) {
	return model.RichPage{Part: true, Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: model.RichText{Text: "IV"}}}}, s.err
}

// The Instant View of a preview opens in an
// article window titled with the site, the link's to open in a browser;
// one that cannot be loaded is told of.
func TestInstantViewOpens(t *testing.T) {
	m := model.Message{Key: model.MessageKey{ChatID: 100, MessageID: 3}, Text: "https://example.org/a", WebPage: &model.WebPreview{URL: "https://example.org/a", Site: "Example", InstantView: true}}
	store := &ivStore{}
	h := newEntityHarness(t, m, model.KindUser)
	h.page.source = store
	var opened model.Message
	var title string
	var src articleSource
	h.page.openSourceWindow = func(article model.Message, name string, s articleSource) { opened, title, src = article, name, s }
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for h.page.sourceBusy.Load() || len(h.page.sourceDone) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the view did not come")
			}
			time.Sleep(5 * time.Millisecond)
		}
		h.page.sourceEvents(h.l)
	}
	h.page.openInstantView(m)
	wait()
	if opened.Rich == nil || opened.Rich.Part || opened.Rich.Blocks[0].Text.Text != "IV" || title != "Example" || src.url != m.WebPage.URL {
		t.Fatalf("opened %+v as %q from %+v", opened.Rich, title, src)
	}
	store.err = errors.New("offline")
	opened = model.Message{}
	h.page.openInstantView(m)
	wait()
	if opened.Rich != nil || h.page.toast.Text() == "" {
		t.Fatal("a view that cannot be loaded opened, or nothing was told")
	}
}
