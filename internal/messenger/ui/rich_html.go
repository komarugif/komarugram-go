// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gioui.org/io/clipboard"
	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/formula"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/richhtml"
)

// A rich message is saved as HTML as Telegram Desktop saves one
// (Iv::RichMessageHtmlExport): the whole message, a folder in the user's
// downloads named after its title, the page in it under the same name and
// its media in media/ beside it, its formulas as SVG in the page. Media
// that cannot be had shows "Media unavailable"; a page that cannot be
// written leaves nothing behind. Copying text selected in an article puts
// the blocks it is in on the clipboard as HTML too, the first and the last
// cut to the selection when they are text, as Telegram Desktop's
// RichPageBlocksForSelectedSegments cuts them.

// htmlSaved is what saving a message as HTML came to: the page written, or
// why it was not.
type htmlSaved struct {
	path string
	err  error
}

// canSaveHTML reports whether m may be saved as HTML: a rich message that
// may be forwarded, as in Telegram Desktop.
func canSaveHTML(m model.Message) bool {
	return m.Rich != nil && !m.NoForwards && len(m.Rich.Blocks) > 0
}

// saveHTML saves m as HTML off the frame; htmlEvents tells how it went.
func (p *chatPage) saveHTML(m model.Message, l localization.Catalog) {
	if p.htmlBusy.Swap(true) {
		return
	}
	if p.htmlDone == nil {
		p.htmlDone = make(chan htmlSaved, 1)
	}
	done := p.htmlDone
	go func() {
		defer p.invalidate()
		defer p.htmlBusy.Store(false)
		var result htmlSaved
		defer func() { done <- result }()
		defer crash.Recover("save as HTML", func(e *crash.Panic) { result.err = e })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		page := *m.Rich
		if rich, ok := p.source.(model.RichStore); ok && page.Part {
			// The whole message, as Telegram Desktop resolves it first; the
			// part when it cannot be had.
			if whole, err := rich.RichMessage(ctx, m.Key); err == nil && len(whole.Blocks) > 0 {
				page = whole
			}
		}
		result.path, result.err = p.writeHTML(ctx, m, page, downloadsDir(), l)
	}()
}

// htmlEvents tells how saving a message as HTML went.
func (p *chatPage) htmlEvents(l localization.Catalog) {
	if p.htmlDone == nil {
		return
	}
	select {
	case got := <-p.htmlDone:
		if got.err != nil {
			p.toast.Show(l.T("rich.html_failed"))
		} else {
			p.toast.Show(l.Format("rich.html_saved", map[string]string{"path": got.path}))
		}
	default:
	}
}

// writeHTML writes page, of message m, in a folder of its own in dir, and
// returns the page's path.
func (p *chatPage) writeHTML(ctx context.Context, m model.Message, page model.RichPage, dir string, l localization.Catalog) (string, error) {
	title := richhtml.Title(page)
	name := fileNamePart(title)
	if name == "" {
		name = "message"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	folder := ""
	for i := range 100 {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)", name, i+1)
		}
		// Mkdir fails on a folder there is: one never takes another's place.
		if err := os.Mkdir(filepath.Join(dir, candidate), 0o755); err == nil {
			folder, name = filepath.Join(dir, candidate), candidate
			break
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	if folder == "" {
		return "", fmt.Errorf("no free folder for %q in %s", name, dir)
	}
	written := false
	defer func() {
		if !written {
			_ = os.RemoveAll(folder)
		}
	}()
	media := map[string]string{}
	used := map[string]bool{}
	for i, rm := range richhtml.Media(page) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rel, err := p.saveMediaFile(ctx, mediaMessage(m, rm), folder, i+1, used)
		if err == nil {
			media[rm.Media.ID] = rel
		}
	}
	if title == "" {
		title = name
	}
	html := richhtml.Page(page, richhtml.Options{
		Title:   title,
		Lang:    string(l.Language()),
		Media:   media,
		Missing: l.T("rich.html_media_missing"),
		Embed:   l.T("rich.html_embed"),
		Formula: func(tex string, display bool) string {
			r, err := formula.Wait(ctx, tex, display)
			if err != nil || r.List == nil {
				return ""
			}
			return r.List.SVG("currentColor", tex)
		},
		Date: func(e model.Entity) string {
			s, _ := formattedDate(l, time.Unix(e.Date, 0), time.Now(), e.DateFormat)
			return s
		},
		Day: func(t time.Time) string { return dayOfMonth(l, t.Local(), time.Now(), true) },
	})
	path := filepath.Join(folder, name+".html")
	if err := os.WriteFile(path, html, 0o644); err != nil {
		return "", err
	}
	written = true
	return path, nil
}

// saveMediaFile saves the media of m in folder's media/, named after its
// file or as the n-th of its kind, a name not used yet, and returns its
// path from folder, with slashes.
func (p *chatPage) saveMediaFile(ctx context.Context, m model.Message, folder string, n int, used map[string]bool) (string, error) {
	dir := filepath.Join(folder, "media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var data []byte
	var stream io.ReaderAt
	var size int64
	if source, ok := p.source.(interface {
		MediaStream(context.Context, model.Message) (io.ReaderAt, int64, func(), error)
	}); ok && m.Kind != model.MessagePhoto {
		r, n, closeStream, err := source.MediaStream(ctx, m)
		if err != nil {
			return "", err
		}
		defer closeStream()
		stream, size = r, n
	} else {
		var err error
		if data, err = p.source.Media(ctx, m); err != nil {
			return "", err
		}
	}
	ext := filepath.Ext(m.Media.FileName)
	if ext == "" {
		head := data
		if stream != nil {
			head = make([]byte, min(size, 512))
			k, _ := stream.ReadAt(head, 0)
			head = head[:k]
		}
		ext = mediaExtension(m.Media.MIMEType, head, m.Kind)
	}
	name := fileNamePart(strings.TrimSuffix(filepath.Base(strings.ReplaceAll(m.Media.FileName, "\\", "/")), filepath.Ext(m.Media.FileName)))
	if name == "" || name == "." {
		kind := "file"
		switch m.Kind {
		case model.MessagePhoto:
			kind = "photo"
		case model.MessageVideo, model.MessageGIF:
			kind = "video"
		case model.MessageMusic, model.MessageVoice:
			kind = "audio"
		}
		name = fmt.Sprintf("%s_%d", kind, n)
	}
	file := name + ext
	if used[strings.ToLower(file)] {
		file = fmt.Sprintf("%s_%d%s", name, n, ext)
	}
	used[strings.ToLower(file)] = true
	path := filepath.Join(dir, file)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if stream != nil {
		_, err = io.Copy(out, io.NewSectionReader(stream, 0, size))
	} else {
		_, err = out.Write(data)
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return "media/" + file, nil
}

// mediaExtension is the extension of media of type mimeType, whose content
// begins with head.
func mediaExtension(mimeType string, head []byte, kind model.MessageKind) string {
	known := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif", "video/mp4": ".mp4", "audio/mpeg": ".mp3", "audio/ogg": ".ogg", "audio/mp4": ".m4a", "video/webm": ".webm"}
	for _, t := range []string{mimeType, http.DetectContentType(head)} {
		t, _, _ = strings.Cut(t, ";")
		if ext, ok := known[t]; ok {
			return ext
		}
		if exts, _ := mime.ExtensionsByType(t); len(exts) > 0 && t != "application/octet-stream" && t != "text/plain" {
			return exts[0]
		}
	}
	if kind == model.MessagePhoto {
		return ".jpg"
	}
	return ""
}

// fileNamePart is s as a part of a file's name on every system: without
// the characters one forbids, nor spaces and dots at its ends, at most 64
// letters, and never a name Windows keeps for a device.
func fileNamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		case strings.ContainsRune(`<>:"/\|?*`, r), unicode.IsControl(r), r == utf8.RuneError:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), " .")
	if utf8.RuneCountInString(out) > 64 {
		out = strings.TrimRight(string([]rune(out)[:64]), " .")
	}
	base, _, _ := strings.Cut(strings.ToUpper(out), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		out = "_" + out
	}
	return out
}

// selectedBlocks are the blocks of the page that runes lo to hi of the
// article's text are in, the first and the last cut to them when they are
// a text.
func (d *articleDoc) selectedBlocks(lo, hi int) []model.RichBlock {
	first, last := -1, -1
	for i, top := range d.tops {
		if top.end > lo && top.start < hi || top.start == top.end && top.start > lo && top.start < hi {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || first >= len(d.page.Blocks) {
		return nil
	}
	out := make([]model.RichBlock, 0, last-first+1)
	for i := first; i <= last; i++ {
		b := d.page.Blocks[i]
		if leaf := d.tops[i].leaf; leaf >= 0 {
			start := d.leaves[leaf].runeStart
			text := b.Text.Text
			from, to := max(lo-start, 0), min(hi-start, utf8.RuneCountInString(text))
			if from > 0 || to < utf8.RuneCountInString(text) {
				b.Text = b.Text.Slice(runeByte(text, from), runeByte(text, to))
				b.Anchor = ""
			}
		}
		out = append(out, b)
	}
	return out
}

// runeByte is the byte of s at rune n.
func runeByte(s string, n int) int {
	for i := range s {
		if n == 0 {
			return i
		}
		n--
	}
	return len(s)
}

// selectedHTML is the selection of article row r as HTML, nil when r is no
// article or nothing is selected.
func (r *messageRow) selectedHTML(l localization.Catalog) []byte {
	if r.article == nil || r.noCopy {
		return nil
	}
	lo, hi := min(r.text.anchor, r.text.caret), max(r.text.anchor, r.text.caret)
	blocks := r.article.selectedBlocks(lo, hi)
	if len(blocks) == 0 {
		return nil
	}
	return richhtml.Page(model.RichPage{RTL: r.article.page.RTL, Blocks: blocks}, richhtml.Options{
		Lang:         string(l.Language()),
		HideSpoilers: !r.revealed,
		SkipMissing:  true,
		// A clipboard takes no files: the formulas laid out go in as
		// pictures of themselves, the others as their source.
		Formula: func(tex string, display bool) string {
			res, ok := formula.Lookup(formula.KeyOf(tex, display))
			if !ok || res.List == nil {
				return ""
			}
			svg := base64.StdEncoding.EncodeToString([]byte(res.List.SVG("#000", "")))
			return fmt.Sprintf(`<img alt="%s" src="data:image/svg+xml;base64,%s">`, strings.NewReplacer(`&`, "&amp;", `"`, "&#34;", `<`, "&lt;").Replace(tex), svg)
		},
		Date: func(e model.Entity) string {
			s, _ := formattedDate(l, time.Unix(e.Date, 0), time.Now(), e.DateFormat)
			return s
		},
		Day: func(t time.Time) string { return dayOfMonth(l, t.Local(), time.Now(), true) },
	})
}

// copySelection puts the text selected in r on the clipboard, with its
// HTML when r is an article, in the language its dates are written in.
func copySelection(gtx layout.Context, r *messageRow) {
	text := r.selectedText()
	if text == "" {
		return
	}
	gtx.Execute(clipboard.WriteCmd{Type: clipboard.TypeText, Data: io.NopCloser(strings.NewReader(text)), HTML: r.selectedHTML(localization.For(string(r.language)))})
}
