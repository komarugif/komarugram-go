// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"

	"gio-mw/token"

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
	busy   bool
}

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

func (p *chatPage) openAttachment(m model.Message) {
	if p.files == nil {
		ctx, cancel := context.WithCancel(context.Background())
		p.files = &attachmentFiles{ctx: ctx, cancel: cancel}
	}
	f := p.files
	f.mu.Lock()
	if f.busy {
		f.mu.Unlock()
		return
	}
	f.busy = true
	f.mu.Unlock()
	p.reportMedia(nil)
	f.wg.Go(func() {
		defer func() { f.mu.Lock(); f.busy = false; f.mu.Unlock(); p.invalidate() }()
		defer crash.Recover("open attachment", func(e *crash.Panic) { p.reportMedia(e) })
		ctx, cancel := context.WithTimeout(f.ctx, 10*time.Minute)
		defer cancel()
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
		out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			p.reportMedia(err)
			return
		}
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
		if closeErr := out.Close(); err == nil {
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
		p.reportMedia(err)
	})
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
	busy := false
	if p.files != nil {
		p.files.mu.Lock()
		busy = p.files.busy
		p.files.mu.Unlock()
	}
	details := sendfiles.Size(m.Media.Size)
	icon := iconFileRow
	if playing {
		details = m.Media.Duration.Round(time.Second).String()
		icon = iconPlayFile
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
				layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if busy {
						return p.fileLoader.sized(gtx, l, 26)
					}
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
func pollLayout(gtx layout.Context, p *model.Poll, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, p.Question, token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
	})}
	for _, a := range p.Answers {
		rows = append(rows, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			mark := "○ "
			if a.Chosen {
				mark = "● "
			}
			if a.Correct {
				mark = "✓ "
			}
			pct := 0
			if p.Total > 0 {
				pct = a.Voters * 100 / p.Total
			}
			return label(gtx, fmt.Sprintf("%s%s   %d%%", mark, a.Text, pct), token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
		}))
	}
	rows = append(rows, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		state := l.T("poll.open")
		if p.Quiz {
			state = l.T("poll.quiz")
		}
		if p.Closed {
			state = l.T("poll.closed")
		}
		return label(gtx, l.Count("poll.count", p.Total, nil)+" · "+state, token.TypestyleLabelSmall, sc.SurfaceVariant.OnColor, 2)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
