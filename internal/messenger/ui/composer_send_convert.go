// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gio-mw/token"
	"gio-mw/widget/indicator"
	"gio-mw/widget/radio"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/sendfiles"
	"komarugram/pkg/video"
)

// boxConvert is the dialog over the box of files that asks what to do with videos
// Telegram would not show as videos, and how far making MP4s of them came. It is
// asked only with an FFmpeg to make them.
type boxConvert struct {
	modal           modal
	radios          *radio.Radios[bool]
	convert         bool
	confirm, cancel surface
	bar             *indicator.Indicator
	job             *convertJob
}

// convertJob is the videos being converted, off the frame.
type convertJob struct {
	stop context.CancelFunc
	done chan []convertedFile
	// err is why it ended before its end, read once done has told.
	err   error
	mu    sync.Mutex
	share float64
}

// convertedFile is a file of the box and the MP4 made of it.
type convertedFile struct {
	file *boxFile
	path string
	info sendfiles.File
}

func (j *convertJob) report(share float64) {
	j.mu.Lock()
	j.share = share
	j.mu.Unlock()
}

func (j *convertJob) progress() float64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.share
}

// toConvert are the files of the box to ask about: videos going as videos in what
// Telegram does not keep as videos, and only when there is an FFmpeg that makes H.264
// to convert them and an ffprobe to tell what they are.
func (c *messageComposer) toConvert() []*boxFile {
	b := &c.files
	if b.way.Documents {
		return nil
	}
	var files []*boxFile
	for _, f := range b.files {
		if f.Kind == sendfiles.KindVideo && !sendfiles.SentAsVideo(f.MIME) {
			files = append(files, f)
		}
	}
	if len(files) == 0 || !video.CanEncodeH264(c.ffmpegPath()) || video.ResolveFFprobe(c.ffmpegPath()) == "" {
		return nil
	}
	return files
}

// askConvert opens the dialog, converting chosen first: what Telegram shows to all.
func (d *boxConvert) askConvert() {
	d.convert = true
	d.radios = radio.NewRadios([]bool{true, false}, true, func(convert bool) { d.convert = convert })
	d.job = nil
	d.modal.Open()
}

// startConvert makes MP4s of files, each in a directory of its own, as a picture
// pasted has, and removed as it is.
func (c *messageComposer) startConvert(files []*boxFile) {
	d := &c.files.convert
	ctx, cancel := context.WithCancel(c.ctx)
	job := &convertJob{stop: cancel, done: make(chan []convertedFile, 1), share: -1}
	d.job, d.bar = job, indicator.Linear()
	ffmpeg, invalidate := c.ffmpegPath(), c.invalidate
	type source struct {
		file *boxFile
		path string
	}
	sources := make([]source, len(files))
	for i, f := range files {
		sources[i] = source{f, f.Path}
	}
	go func() {
		var made []convertedFile
		defer invalidate()
		defer func() { job.done <- made }()
		defer crash.Recover("convert video", func(p *crash.Panic) { job.err = p })
		for i, s := range sources {
			m, err := convertOne(ctx, ffmpeg, s.path, func(share float64) {
				if share < 0 {
					job.report(-1)
				} else {
					job.report((float64(i) + share) / float64(len(sources)))
				}
				invalidate()
			})
			if err != nil {
				job.err = err
				return
			}
			m.file = s.file
			made = append(made, m)
		}
	}()
}

// convertOne makes an MP4 of the video at path, named as it is.
func convertOne(ctx context.Context, ffmpeg, path string, report func(float64)) (convertedFile, error) {
	dir, err := os.MkdirTemp("", "komarugram-go-convert-*")
	if err != nil {
		return convertedFile{}, err
	}
	name := filepath.Base(path)
	out := filepath.Join(dir, strings.TrimSuffix(name, filepath.Ext(name))+".mp4")
	err = video.ConvertToMP4(ctx, ffmpeg, path, out, func(p video.GIFProgress) { report(p.Share()) })
	var info sendfiles.File
	if err == nil {
		info, err = sendfiles.Inspect(out)
	}
	if err != nil {
		os.RemoveAll(dir)
		if ctx.Err() != nil {
			// A process stopped says only that it was killed: this is what happened.
			err = ctx.Err()
		}
		return convertedFile{}, err
	}
	return convertedFile{path: out, info: info}, nil
}

// takeConverted takes what the job made. What it made of a file no longer in the box
// goes; the rest take those files' places, and with nothing gone wrong, what the box
// holds is sent. It is asked with the box closed too: it must leave nothing behind.
func (c *messageComposer) takeConverted(l localization.Catalog) {
	b := &c.files
	d := &b.convert
	if d.job == nil {
		return
	}
	var made []convertedFile
	select {
	case made = <-d.job.done:
	default:
		return
	}
	err := d.job.err
	d.job = nil
	shown := b.modal.Shown() && !b.modal.closing
	for _, m := range made {
		found := false
		for _, f := range b.files {
			found = found || f == m.file
		}
		if !shown || !found {
			os.RemoveAll(filepath.Dir(m.path))
			continue
		}
		c.replaceBoxPath(m.file.Path, m.path)
		m.file.File = m.info
	}
	d.modal.Hide()
	d.modal = modal{}
	b.modal.focus = true
	if !shown {
		return
	}
	switch {
	case err == nil:
		c.sendFiles(l)
	case !errors.Is(err, context.Canceled):
		text := mediaErrorText(err)
		if errors.Is(err, video.ErrNoH264Encoder) {
			text = l.T("ffmpeg.no_h264_encoder")
		}
		b.modal.Toast(l.T("files.convert_failed") + ": " + text)
	}
}

// layoutConvert draws the dialog over the box, and does what its buttons ask.
func (c *messageComposer) layoutConvert(gtx layout.Context, l localization.Catalog) {
	d := &c.files.convert
	if !d.modal.Shown() {
		return
	}
	if d.cancel.Clicked(gtx) {
		if d.job != nil {
			d.job.stop()
		} else {
			d.modal.Close()
		}
	}
	if d.confirm.Clicked(gtx) && d.job == nil {
		if files := c.toConvert(); d.convert && len(files) > 0 {
			c.startConvert(files)
		} else {
			d.modal.Hide()
			c.files.modal.focus = true
			c.submitFiles()
			return
		}
	}
	if d.job == nil {
		// Here, not at the opening: the radios refuse to be drawn in their first
		// frame without it.
		d.radios.Update(gtx)
	}
	shown := d.modal.Layout(gtx, d.job != nil, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, d.rows(gtx, l)...)
		}, defaultCardPadding)
	})
	if !shown {
		d.modal = modal{}
		c.files.modal.focus = true
	}
	if d.job != nil {
		gtx.Execute(op.InvalidateCmd{})
	}
}

// rows are what the dialog shows.
func (d *boxConvert) rows(gtx layout.Context, l localization.Catalog) []layout.FlexChild {
	sc := scheme(gtx)
	title := layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.T("files.convert_title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 2)
	})
	buttons := func(ws ...layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			children := make([]layout.FlexChild, len(ws))
			for i, w := range ws {
				children[i] = layout.Rigid(w)
			}
			return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx, children...)
		})
	}
	cancel := func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.cancel, l.T("history.cancel")) }
	if d.job != nil {
		rows := []layout.FlexChild{title, vspace(12)}
		rows = append(rows, progressRows(d.bar, l.T("files.converting"), float32(d.job.progress()))...)
		return append(rows, vspace(8), buttons(cancel))
	}
	return []layout.FlexChild{
		title, vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("files.convert_about"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return d.radios.Layout(gtx, radio.LeadingKind, map[bool]string{
				true:  l.T("files.convert_mp4"),
				false: l.T("files.convert_keep"),
			})
		}),
		vspace(8),
		buttons(cancel, func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.confirm, l.T("editor.continue")) }),
	}
}
