// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// Exported attachments live in a private temporary directory only while this
// window is open. The encrypted history/media cache is never handed to helpers.
type attachmentFiles struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	dir    string
	// downloads are the files on their way, by message: each its own,
	// cancelled by a click on its row.
	downloads map[model.MessageKey]*fileDownload
	// finished counts the downloads that ended, for the Downloads section
	// to read its list again.
	finished atomic.Int64
	// shared is set on the window's, which its pages share and the window
	// closes: downloads go on from one chat to another.
	shared bool
}

// newAttachmentFiles is the downloads of a window, which its pages share.
func newAttachmentFiles() *attachmentFiles {
	ctx, cancel := context.WithCancel(context.Background())
	return &attachmentFiles{ctx: ctx, cancel: cancel, shared: true}
}

// activeDownload is a file on its way, as the Downloads section shows it.
type activeDownload struct {
	message     model.Message
	done, total int64
	started     time.Time
}

// active are the files on their way, the first started first.
func (f *attachmentFiles) active() []activeDownload {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]activeDownload, 0, len(f.downloads))
	for _, d := range f.downloads {
		out = append(out, activeDownload{message: d.message, done: d.done, total: d.total, started: d.started})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].started.Before(out[j].started) })
	return out
}

// cancelDownload stops the download of key, which then tells nothing.
func (f *attachmentFiles) cancelDownload(key model.MessageKey) {
	f.mu.Lock()
	d := f.downloads[key]
	f.mu.Unlock()
	if d != nil {
		d.cancelled.Store(true)
		d.cancel()
	}
}

// fileDownload is a file on its way to be opened: done of its total bytes
// are there.
type fileDownload struct {
	message     model.Message
	started     time.Time
	done, total int64
	cancel      context.CancelFunc
	// cancelled is set when the user cancelled it, which tells nothing.
	cancelled atomic.Bool
	// told is when the page was last asked to draw it.
	told time.Time
}

// download is the file of message key on its way, nil when there is none.
func (f *attachmentFiles) download(key model.MessageKey) (done, total int64, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.downloads[key]
	if d == nil {
		return 0, 0, false
	}
	return d.done, d.total, true
}

// progressWriter counts what goes through it into d, and has the page drawn
// again at most 30 times a second.
type progressWriter struct {
	w          io.Writer
	f          *attachmentFiles
	d          *fileDownload
	invalidate func()
}

func (w *progressWriter) Write(b []byte) (int, error) {
	n, err := w.w.Write(b)
	w.f.mu.Lock()
	w.d.done += int64(n)
	now := time.Now()
	tell := now.Sub(w.d.told) >= time.Second/30
	if tell {
		w.d.told = now
	}
	w.f.mu.Unlock()
	if tell {
		w.invalidate()
	}
	return n, err
}

// progress tells that done of total bytes of d are there, and has the page
// drawn again at most 30 times a second.
func (f *attachmentFiles) progress(d *fileDownload, done, total int64, invalidate func()) {
	f.mu.Lock()
	d.done, d.total = done, total
	now := time.Now()
	tell := now.Sub(d.told) >= time.Second/30 || done == total
	if tell {
		d.told = now
	}
	f.mu.Unlock()
	if tell {
		invalidate()
	}
}

// appDownloadsDir is where the client downloads into: its own directory in
// the user's downloads.
func appDownloadsDir() string { return filepath.Join(downloadsDir(), "komarugram") }

func (f *attachmentFiles) Close() {
	f.cancel()
	f.wg.Wait()
	if f.dir != "" {
		_ = os.RemoveAll(f.dir)
	}
}

// directory is the window's private temporary directory, made the first
// time it is asked for.
func (f *attachmentFiles) directory() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dir == "" {
		dir, err := os.MkdirTemp("", "komarugram-go-attachments-")
		if err != nil {
			return "", err
		}
		f.dir = dir
	}
	return f.dir, nil
}

// openAttachment downloads the file of m and opens it in the system's
// program for it; while it downloads, it cancels that instead.
func (p *chatPage) openAttachment(m model.Message) {
	p.downloadFile(m, true, "")
}

// downloadFile downloads the file of m into the user's downloads, and
// opens it after when open is set, or tells saved, a text with {path}, in
// the toast; while it downloads, it cancels that instead.
func (p *chatPage) downloadFile(m model.Message, open bool, saved string) {
	if p.files == nil {
		ctx, cancel := context.WithCancel(context.Background())
		p.files = &attachmentFiles{ctx: ctx, cancel: cancel}
	}
	f := p.files
	f.mu.Lock()
	if d := f.downloads[m.Key]; d != nil {
		f.mu.Unlock()
		d.cancelled.Store(true)
		d.cancel()
		return
	}
	ctx, cancel := context.WithCancel(f.ctx)
	d := &fileDownload{message: m, started: time.Now(), total: m.Media.Size, cancel: cancel}
	if f.downloads == nil {
		f.downloads = map[model.MessageKey]*fileDownload{}
	}
	f.downloads[m.Key] = d
	f.mu.Unlock()
	p.reportMedia(nil)
	f.wg.Go(func() {
		defer func() {
			cancel()
			f.mu.Lock()
			delete(f.downloads, m.Key)
			f.mu.Unlock()
			f.finished.Add(1)
			p.invalidate()
		}()
		defer crash.Recover("open attachment", func(e *crash.Panic) { p.reportMedia(e) })
		if downloader, ok := p.source.(model.Downloader); ok {
			// As Telegram Desktop does: into the user's downloads, where
			// the file stays theirs, and opened from there.
			path, err := downloader.Download(ctx, m, appDownloadsDir(), func(done, total int64) {
				f.progress(d, done, total, p.invalidate)
			})
			if err == nil {
				err = ctx.Err()
			}
			if err == nil && open {
				err = openBrowser(path)
			}
			if d.cancelled.Load() {
				err = nil
			} else if err == nil && saved != "" {
				p.reportNotice(strings.ReplaceAll(saved, "{path}", path))
				return
			}
			p.reportMedia(err)
			return
		}
		dir, err := f.directory()
		if err != nil {
			p.reportMedia(err)
			return
		}
		name := filepath.Base(strings.ReplaceAll(m.Media.FileName, "\\", "/"))
		if name == "" || name == "." || name == "/" {
			name = "attachment"
		}
		// Prefix the message ID to keep equal filenames from replacing one another.
		path := filepath.Join(dir, fmt.Sprintf("%d-%d-%s", m.Key.ChatID, m.Key.MessageID, name))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			p.reportMedia(err)
			return
		}
		out := &progressWriter{w: file, f: f, d: d, invalidate: p.invalidate}
		if source, ok := p.source.(interface {
			MediaStream(context.Context, model.Message) (io.ReaderAt, int64, func(), error)
		}); ok {
			var r io.ReaderAt
			var n int64
			var closeStream func()
			r, n, closeStream, err = source.MediaStream(ctx, m)
			if err == nil {
				_, err = io.Copy(out, io.NewSectionReader(r, 0, n))
				closeStream()
			}
		} else {
			var data []byte
			data, err = p.source.Media(ctx, m)
			if err == nil {
				_, err = out.Write(data)
			}
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = openBrowser(path)
		} else {
			_ = os.Remove(path)
		}
		if d.cancelled.Load() {
			// Cancelled by the user: nothing to tell.
			err = nil
		}
		p.reportMedia(err)
	})
}

// reportNotice tells text, the outcome of a task in the background, in the
// toast; it may be called from any goroutine.
func (p *chatPage) reportNotice(text string) {
	p.errorMu.Lock()
	p.notice = text
	p.errorMu.Unlock()
	p.invalidate()
}

func (p *chatPage) reportMedia(err error) {
	p.errorMu.Lock()
	p.mediaError = err
	p.errorMu.Unlock()
	p.invalidate()
}
func (p *chatPage) fileLayout(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) layout.Dimensions {
	if internalAudio(m) && (p.audioExternal == nil || !p.audioExternal()) {
		if m.Kind == model.MessageMusic {
			return p.musicLayout(gtx, r, m, l)
		}
		return p.voiceLayout(gtx, r, m, l)
	}
	playing := m.Kind == model.MessageVoice || m.Kind == model.MessageMusic
	if r.media.Clicked(gtx) {
		if playing {
			p.play(gtx, m, p.reportMedia, l)
		} else if markdownFile(m) {
			p.openMarkdown(m)
		} else {
			p.openAttachment(m)
		}
	}
	title := m.Media.FileName
	if m.Media.Title != "" {
		title = m.Media.Title
		if m.Media.Performer != "" {
			title = m.Media.Performer + " — " + title
		}
	}
	if title == "" && m.Kind == model.MessageVoice {
		title = l.T("history.voice")
	}
	if title == "" {
		title = l.T("history.file")
	}
	var done, total int64
	downloading := false
	if p.files != nil {
		done, total, downloading = p.files.download(m.Key)
	}
	details := sendfiles.Size(m.Media.Size)
	icon := iconFileRow
	if playing {
		details = m.Media.Duration.Round(time.Second).String()
		icon = iconPlayFile
	}
	// progress is below 0 while the size is not known.
	progress := float32(-1)
	if downloading {
		details = sendfiles.Size(done)
		if total > 0 {
			progress = min(1, float32(done)/float32(total))
			details += " / " + sendfiles.Size(total)
		}
	}
	// As materialgram draws files: a round button in the primary color,
	// the name and the size beside it.
	return r.media.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		sc := scheme(gtx)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				d := gtx.Dp(44)
				size := image.Pt(d, d)
				paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse{Max: size}.Op(gtx.Ops))
				gtx.Constraints = layout.Exact(size)
				if downloading {
					// A click on the row cancels it.
					return r.ring.layout(gtx, d, true, progress, iconClear, sc.Primary.OnColor, wdk.AnimationsEnabled(gtx))
				}
				r.ring.reset()
				layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(gtx.Dp(24), gtx.Dp(24)), func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, sc.Primary.OnColor)
					})
				})
				return layout.Dimensions{Size: size}
			}),
			layout.Rigid(layout.Spacer{Width: 12}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, title, token.TypestyleBodyLargeEmphasized, sc.Surface.OnColor, 2)
					}),
					vspace(2),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, details, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
					}),
				)
			}),
		)
	})
}
