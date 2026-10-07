// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/markdown"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/cmark"
)

// A Markdown file opens in the client, as Telegram Desktop opens one
// (Iv::Instance::showMarkdown): its article in a window of its own, named
// after the file, with "Open file" for the system's program. One the
// client cannot show, too large or the parser failed on it, is told so
// and opens in the system's program. An Instant View opens the same way
// (web_preview.go).

// articleSource is what an article window shows the article of, when it
// is not a rich message: a Markdown file, or the page at url.
type articleSource struct {
	file *model.Message
	url  string
}

// sourceOpened is the article of a source made, or why it could not be.
type sourceOpened struct {
	src     articleSource
	article model.Message
	title   string
	err     error
}

// markdownFile reports whether m is a Markdown file the client shows.
func markdownFile(m model.Message) bool {
	if m.Media == nil || m.Media.Size > int64(cmark.DefaultLimits.MaxSource) {
		return false
	}
	mime := strings.ToLower(m.Media.MIMEType)
	return markdown.IsMarkdownName(m.Media.FileName) || mime == "text/markdown" || mime == "text/x-markdown"
}

// openSource makes, off the frame, the article build returns; sourceEvents
// shows it.
func (p *chatPage) openSource(src articleSource, build func(ctx context.Context) (model.Message, string, error)) {
	if p.sourceBusy.Swap(true) {
		return
	}
	if p.sourceDone == nil {
		p.sourceDone = make(chan sourceOpened, 1)
	}
	done := p.sourceDone
	go func() {
		defer p.invalidate()
		defer p.sourceBusy.Store(false)
		result := sourceOpened{src: src}
		defer func() { done <- result }()
		defer crash.Recover("open article", func(e *crash.Panic) { result.err = e })
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result.article, result.title, result.err = build(ctx)
	}()
}

// openMarkdown prepares Markdown file m off the frame.
func (p *chatPage) openMarkdown(m model.Message) {
	p.openSource(articleSource{file: &m}, func(ctx context.Context) (model.Message, string, error) {
		data, err := p.source.Media(ctx, m)
		if err != nil {
			return model.Message{}, "", err
		}
		rt, err := cmark.NewRuntime(ctx, cmark.DefaultLimits)
		if err != nil {
			return model.Message{}, "", err
		}
		defer rt.Close(ctx)
		// A file from a chat has no files beside it: its relative links
		// go nowhere.
		doc, err := markdown.Prepare(ctx, rt.Parse, data, "")
		if err != nil {
			return model.Message{}, "", err
		}
		title := filepath.Base(m.Media.FileName)
		if m.Media.FileName == "" {
			title = doc.Title
		}
		return articleMessage(m, doc.Page), title, nil
	})
}

// sourceEvents shows the article made last; a Markdown file that could
// not be shown opens in the system's program.
func (p *chatPage) sourceEvents(l localization.Catalog) {
	if p.sourceDone == nil {
		return
	}
	select {
	case got := <-p.sourceDone:
		switch {
		case got.err == nil && p.openSourceWindow != nil:
			p.openSourceWindow(got.article, got.title, got.src)
		case got.src.file != nil:
			if got.err != nil {
				p.toast.Show(l.T("rich.markdown_cant"))
			}
			p.openAttachment(*got.src.file)
		case got.err != nil:
			p.toast.Show(mediaErrorText(got.err))
		}
	default:
	}
}

// articleMessage is page, the article of message of, as a message the
// article window shows: of's, so that sharing it shares of.
func articleMessage(of model.Message, page model.RichPage) model.Message {
	summary := page.Summary()
	m := model.Message{Key: of.Key, Kind: model.MessageText, Date: of.Date, SenderID: of.SenderID, Text: summary.Text, Entities: summary.Entities, Rich: &page, NoForwards: of.NoForwards}
	m.ContentRevision = model.Revision(m)
	return m
}
