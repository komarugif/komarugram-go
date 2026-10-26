// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/widget/indicator"
	"gio-mw/widget/radio"

	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/savefile"
	"komarugram/pkg/tgswebm"
)

// stickerVideoDialog saves a Lottie sticker as a video: it asks for the
// quality, then where, and shows how far the conversion has come, which
// takes seconds. A sticker is mostly saved for a video editor, which takes
// any size: the frame rate matters there, and the size only to send it as
// a video sticker again.
type stickerVideoDialog struct {
	modal     modal
	msg       model.Message
	quality   stickerVideoQuality
	qualities *radio.Radios[stickerVideoQuality]
	bar       *indicator.Indicator
	cancel    surface
	save      surface
	// work is the work under way, nil while there is none.
	work *stickerVideoWork
}

// stickerVideoWork is a conversion under way, which tells how far it has
// come from its goroutine.
type stickerVideoWork struct {
	stop context.CancelFunc
	done chan stickerVideoResult
	mu   sync.Mutex
	// choosing is set while the system's dialog asks where; started, once
	// a place was chosen.
	choosing, started bool
	progress          tgswebm.Progress
}

func (w *stickerVideoWork) state() (choosing, started bool, progress tgswebm.Progress) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.choosing, w.started, w.progress
}

// stickerVideoQuality is what a sticker's video is made as.
type stickerVideoQuality int

const (
	// stickerVideo60 is 60 frames a second, as Telegram plays the sticker.
	stickerVideo60 stickerVideoQuality = iota
	stickerVideo30
	// stickerVideoSendable is a video sticker as Telegram takes them: 30
	// frames a second, within 256 KiB.
	stickerVideoSendable
)

func (q stickerVideoQuality) options() tgswebm.Options {
	switch q {
	case stickerVideo30:
		return tgswebm.Options{MaxFPS: 30, MaxBytes: -1}
	case stickerVideoSendable:
		return tgswebm.Options{MaxFPS: 30}
	}
	return tgswebm.Options{MaxFPS: 60, MaxBytes: -1}
}

// suffix tells the qualities' files apart, after the sticker's name.
func (q stickerVideoQuality) suffix() string {
	switch q {
	case stickerVideo30:
		return "_30fps"
	case stickerVideoSendable:
		return "_30fps_256kb"
	}
	return "_60fps"
}

type stickerVideoResult struct {
	path string
	err  error
}

// stickerVideoBar is where on the bar each part of a conversion ends: the
// frames drawn (about 15% of the time of an encoding), then each encoding.
// One kept within a size may encode again, at a lower quality, up to four
// times: the first takes most of the bar, and the bar never goes back.
var (
	stickerVideoBar        = []float32{.15, 1}
	stickerVideoBarLimited = []float32{.15, .7, .88, .96, 1}
)

func (d *stickerVideoDialog) open(m model.Message) {
	d.close()
	d.msg, d.quality = m, stickerVideo60
	d.qualities = radio.NewRadios([]stickerVideoQuality{stickerVideo60, stickerVideo30, stickerVideoSendable}, stickerVideo60,
		func(q stickerVideoQuality) { d.quality = q })
	d.modal.Open()
}

// close cancels the work, and forgets the dialog.
func (d *stickerVideoDialog) close() {
	if d.work != nil {
		d.work.stop()
	}
	*d = stickerVideoDialog{}
}

func (d *stickerVideoDialog) busy() bool { return d.work != nil }

// begin asks where to save the video, and converts the sticker there.
func (d *stickerVideoDialog) begin(p *chatPage, l localization.Catalog) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	w := &stickerVideoWork{stop: cancel, done: make(chan stickerVideoResult, 1), choosing: true}
	d.work, d.bar = w, indicator.Linear()
	m := d.msg
	name := savefile.Name(m)
	name = strings.TrimSuffix(name, filepath.Ext(name)) + d.quality.suffix() + ".webm"
	kind := l.Format("menu.save_files", map[string]string{"ext": "WEBM"})
	options := d.quality.options()
	options.Progress = func(progress tgswebm.Progress) {
		w.mu.Lock()
		w.progress = progress
		w.mu.Unlock()
		p.invalidate()
	}
	go func() {
		defer crash.Recover("save sticker video", func(e *crash.Panic) { w.done <- stickerVideoResult{err: e} })
		defer p.invalidate()
		path, err := chooseSavePath(ctx, name, kind)
		w.mu.Lock()
		w.choosing, w.started = false, err == nil && path != ""
		w.mu.Unlock()
		if err == nil && path != "" {
			p.invalidate()
			err = p.writeStickerVideo(ctx, m, path, options)
		}
		w.done <- stickerVideoResult{path: path, err: err}
	}()
}

func (d *stickerVideoDialog) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	var done chan stickerVideoResult
	if d.work != nil {
		done = d.work.done
	}
	select {
	case r := <-done:
		d.work.stop()
		d.work = nil
		switch {
		case errors.Is(r.err, context.Canceled):
			// Cancelled: the choice is offered again.
		case r.err != nil:
			d.modal.Toast(mediaErrorText(r.err))
		case r.path != "":
			d.modal.Close()
			p.toast.Show(l.Format("menu.saved", map[string]string{"path": r.path}))
		}
	default:
	}
	if !d.modal.closing {
		if d.cancel.Clicked(gtx) {
			// Cancel stops the work first, and closes the dialog then.
			if d.busy() {
				d.work.stop()
			} else {
				d.modal.Close()
			}
		}
		if d.save.Clicked(gtx) && !d.busy() {
			d.begin(p, l)
		}
		if !d.busy() {
			d.qualities.Update(gtx)
		}
	}
	shown := d.modal.Layout(gtx, d.busy(), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions { return d.layoutContent(gtx, l) }, defaultCardPadding)
	})
	if !shown {
		d.close()
	}
}

func (d *stickerVideoDialog) layoutContent(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	var choosing, started bool
	var progress tgswebm.Progress
	if d.work != nil {
		choosing, started, progress = d.work.state()
	}
	working := started
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("sticker_video.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("sticker_video.about"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(12),
	}
	if working {
		share := stickerVideoShare(progress, d.quality == stickerVideoSendable)
		children = append(children, progressRows(d.bar, stickerVideoStatus(progress, l), share)...)
	} else {
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("sticker_video.quality"), token.TypestyleTitleSmall, sc.Primary.Color, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return d.qualities.Layout(gtx, radio.LeadingKind, map[stickerVideoQuality]string{
					stickerVideo60:       l.T("sticker_video.fps_60"),
					stickerVideo30:       l.T("sticker_video.fps_30"),
					stickerVideoSendable: l.T("sticker_video.fps_30_sendable"),
				})
			}),
		)
	}
	children = append(children, vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Middle, Spacing: layout.SpaceStart}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return textButton(gtx, &d.cancel, l.T("sticker_video.cancel"))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if working || choosing {
						return layout.Dimensions{}
					}
					return textButton(gtx, &d.save, l.T("sticker_video.save"))
				}),
			)
		}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// progressRows are the rows of a dialog that tells how far a conversion
// has come: what it does now, beside the share done in per cent, and the
// bar; a share below 0 is not known, and leaves the bar empty.
func progressRows(bar *indicator.Indicator, status string, share float32) []layout.FlexChild {
	return []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, status, token.TypestyleBodyMedium, sc.Surface.OnColor, 2)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if share < 0 {
						return layout.Dimensions{}
					}
					percent := strconv.Itoa(int(share*100)) + "%"
					return label(gtx, percent, token.TypestyleLabelLarge, sc.SurfaceVariant.OnColor, 1)
				}),
			)
		}),
		vspace(10),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			bar.Progress = max(share, 0)
			return bar.Layout(gtx)
		}),
		vspace(4),
	}
}

// stickerVideoShare is how much of the bar progress fills, along the parts
// of stickerVideoBar, or of stickerVideoBarLimited for a video kept within
// a size.
func stickerVideoShare(progress tgswebm.Progress, limited bool) float32 {
	if progress.Total <= 0 {
		return 0
	}
	ends := stickerVideoBar
	if limited {
		ends = stickerVideoBarLimited
	}
	from, to := float32(0), ends[0]
	if progress.Stage == tgswebm.Encoding {
		part := min(max(progress.Pass, 1), len(ends)-1)
		from, to = ends[part-1], ends[part]
	}
	return from + (to-from)*float32(progress.Done)/float32(progress.Total)
}

// stickerVideoStatus says what the conversion does now.
func stickerVideoStatus(progress tgswebm.Progress, l localization.Catalog) string {
	args := map[string]string{"done": strconv.Itoa(progress.Done), "total": strconv.Itoa(progress.Total), "pass": strconv.Itoa(progress.Pass)}
	switch {
	case progress.Total == 0:
		return l.T("sticker_video.loading")
	case progress.Stage == tgswebm.Drawing:
		return l.Format("sticker_video.drawing", args)
	case progress.Pass > 1:
		return l.Format("sticker_video.encoding_again", args)
	}
	return l.Format("sticker_video.encoding", args)
}

// writeStickerVideo converts m, a Lottie sticker, and writes the video at
// path.
func (p *chatPage) writeStickerVideo(ctx context.Context, m model.Message, path string, options tgswebm.Options) error {
	data, err := p.source.Media(ctx, m)
	if err == nil {
		data, err = tgswebm.Convert(ctx, data, options)
	}
	if err != nil {
		return err
	}
	return writeWhole(ctx, path, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}
