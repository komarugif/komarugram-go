// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/widget/indicator"

	"gioui.org/layout"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/player"
	"komarugram/pkg/video"
)

// viewerGIF saves the video the viewer shows as a GIF among the saved
// ones: converted by FFmpeg when the client has one, or else by
// model.GIFBot. A dialog tells how far it has come, as the one that saves
// a Lottie sticker as a video does, and its button stops it.
type viewerGIF struct {
	maker  model.GIFMaker
	button widget.Clickable
	modal  modal
	bar    *indicator.Indicator
	cancel surface
	// work is the conversion under way, nil while there is none.
	work *gifWork
}

// gifWork is a conversion of a video to a GIF under way, which tells how
// far it has come from its goroutine.
type gifWork struct {
	stop context.CancelFunc
	done chan error
	// bot is set when the bot converts it.
	bot      bool
	mu       sync.Mutex
	progress model.GIFProgress
}

func (w *gifWork) report(p model.GIFProgress) {
	w.mu.Lock()
	w.progress = p
	w.mu.Unlock()
}

func (w *gifWork) state() model.GIFProgress {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.progress
}

// canMakeGIF reports whether the viewer offers to save m as a GIF: a video
// of a message of Telegram's, which the bot can be forwarded.
func (v *mediaViewer) canMakeGIF(m model.Message) bool {
	return v.gif.maker != nil && model.CanMakeGIF(m) && !v.isStory(m) && m.Key.MessageID > 0 && !m.Local()
}

// updateGIF takes the click on the button of m, the item shown, and on the
// dialog's, and what came of the conversion, before anything is drawn.
func (v *mediaViewer) updateGIF(gtx layout.Context, m model.Message, l localization.Catalog) {
	g := &v.gif
	if g.button.Clicked(gtx) && v.canMakeGIF(m) && g.work == nil {
		v.makeGIF(m)
	}
	if g.work == nil {
		return
	}
	if g.cancel.Clicked(gtx) {
		g.work.stop()
	}
	select {
	case err := <-g.work.done:
		g.work.stop()
		g.work = nil
		g.modal.Close()
		if text := gifResultText(err, v.source, l); text != "" {
			v.toast.Show(text)
		}
	default:
	}
}

// makeGIF starts converting m, and opens the dialog.
func (v *mediaViewer) makeGIF(m model.Message) {
	g := &v.gif
	ffmpeg := ""
	if v.ffmpeg != nil {
		ffmpeg = video.ResolveFFmpeg(v.ffmpeg())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	w := &gifWork{stop: cancel, done: make(chan error, 1), bot: ffmpeg == "", progress: model.GIFProgress{Share: -1}}
	g.work, g.bar = w, indicator.Linear()
	g.modal.Open()
	maker, source := g.maker, v.source
	go func() {
		defer crash.Recover("save as GIF", func(e *crash.Panic) { w.done <- e; v.invalidate() })
		report := func(p model.GIFProgress) {
			w.report(p)
			v.invalidate()
		}
		w.done <- makeGIF(ctx, maker, source, m, ffmpeg, report)
		v.invalidate()
	}()
}

// makeGIF converts the video of m with ffmpeg, or by the bot for "", and
// saves the GIF; a video made before saves the GIF it made.
func makeGIF(ctx context.Context, maker model.GIFMaker, source model.ConversationStore, m model.Message, ffmpeg string, report func(model.GIFProgress)) error {
	if gif, ok := maker.MadeGIF(ctx, m); ok {
		report(model.GIFProgress{Stage: model.GIFSending, Share: -1})
		err := maker.SaveGIF(ctx, gif, false)
		if err == nil || errors.Is(err, model.ErrSavedGIFLimit) || ctx.Err() != nil {
			return err
		}
		// Gone from Telegram, it is made again.
	}
	if ffmpeg == "" {
		gif, err := maker.ConvertGIFByBot(ctx, m, report)
		if err != nil {
			return err
		}
		report(model.GIFProgress{Stage: model.GIFSending, Share: -1})
		return maker.SaveGIF(ctx, gif, false)
	}
	f, err := openFile(ctx, source, m)
	if err != nil {
		return err
	}
	defer f.close()
	// FFmpeg reads the file over a loopback stream, as it plays one: it
	// downloads as it is read.
	stream, err := player.Serve("video", f.r, f.size)
	if err != nil {
		return err
	}
	defer stream.Close()
	dir, err := os.MkdirTemp("", "komarugram-go-gif-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, fmt.Sprintf("komarugram-go-gif-%d.mp4", m.Key.MessageID))
	gif, err := video.ConvertGIF(ctx, ffmpeg, stream.URL(), out, func(p video.GIFProgress) {
		report(model.GIFProgress{Stage: model.GIFEncoding, Share: p.Share(), Pass: p.Pass})
	})
	if errors.Is(err, video.ErrGIFTooLarge) {
		return model.ErrGIFTooLarge
	}
	if err != nil {
		return err
	}
	report(model.GIFProgress{Stage: model.GIFSending, Share: -1})
	return maker.SaveGIFFile(ctx, m, out, gif.Width, gif.Height, gif.Duration)
}

// gifResultText tells what came of a conversion: "" for one cancelled.
func gifResultText(err error, source any, l localization.Catalog) string {
	switch {
	case err == nil:
		return l.T("viewer.gif_saved")
	case errors.Is(err, context.Canceled), errors.Is(err, model.ErrGIFBotCancelled):
		return ""
	case errors.Is(err, model.ErrSavedGIFLimit):
		// Saved, in place of the oldest.
		if text := savedGIFLimitText(source, l); text != "" {
			return text
		}
		return l.T("viewer.gif_saved")
	case errors.Is(err, model.ErrGIFTooLarge):
		return l.T("viewer.gif_too_large")
	case errors.Is(err, model.ErrGIFBotSilent), errors.Is(err, context.DeadlineExceeded):
		return l.T("viewer.gif_bot_silent")
	case errors.Is(err, model.ErrGIFBotBusy):
		return l.T("viewer.gif_bot_busy")
	case errors.Is(err, model.ErrGIFBotFailed):
		return l.T("viewer.gif_bot_failed")
	}
	return l.T("viewer.gif_failed") + ": " + mediaErrorText(err)
}

// gifStatus says what the conversion does now.
func gifStatus(p model.GIFProgress, l localization.Catalog) string {
	switch p.Stage {
	case model.GIFQueued:
		return l.Format("viewer.gif_queued", map[string]string{"place": strconv.Itoa(p.Queue)})
	case model.GIFDownloading:
		return l.T("viewer.gif_downloading")
	case model.GIFProbing:
		return l.T("viewer.gif_probing")
	case model.GIFEncoding:
		if p.Pass > 1 {
			return l.Format("viewer.gif_encoding_again", map[string]string{"pass": strconv.Itoa(p.Pass)})
		}
		return l.T("viewer.gif_encoding")
	case model.GIFSending:
		return l.T("viewer.gif_sending")
	}
	return l.T("viewer.gif_starting")
}

// gifShare is how much of the bar a conversion fills: the encoding, which
// takes the time, and all after it; below 0, telling no share, before it
// or when the encoding tells none.
func gifShare(p model.GIFProgress) float32 {
	switch {
	case p.Stage == model.GIFSending:
		return 1
	case p.Stage == model.GIFEncoding && p.Share >= 0:
		return float32(p.Share)
	}
	return -1
}

// layoutGIF draws the dialog over the viewer while it is shown.
func (v *mediaViewer) layoutGIF(gtx layout.Context, l localization.Catalog) {
	g := &v.gif
	if !g.modal.Shown() {
		return
	}
	var progress model.GIFProgress
	bot := false
	if g.work != nil {
		progress, bot = g.work.state(), g.work.bot
	}
	shown := g.modal.Layout(gtx, g.work != nil, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return g.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			about := l.T("viewer.gif_about")
			if bot {
				about += "\n" + l.Format("viewer.gif_about_bot", map[string]string{"bot": "@" + model.GIFBot})
			}
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("viewer.gif_title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, about, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
				}),
				vspace(12),
			}
			children = append(children, progressRows(g.bar, gifStatus(progress, l), gifShare(progress))...)
			children = append(children, vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &g.cancel, l.T("viewer.gif_cancel"))
						}),
					)
				}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		}, defaultCardPadding)
	})
	if !shown {
		// Closed: the keys are the viewer's again.
		v.focus = true
		g.modal = modal{}
	}
}

// closeGIF stops the conversion under way, as the viewer closes.
func (v *mediaViewer) closeGIF() {
	if v.gif.work != nil {
		v.gif.work.stop()
		v.gif.work = nil
	}
	v.gif.modal.Hide()
}
