// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"sync"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// fakeGIFMaker converts by its bot: it tells half the encoding done, then
// waits for a GIF on gifs or for a cancel.
type fakeGIFMaker struct {
	gifs      chan model.Message
	cancelled chan struct{}
	mu        sync.Mutex
	saved     []model.Message
	// made are the GIFs made, by their video; converted counts the bot's.
	made      map[string]model.Message
	converted int
}

func (f *fakeGIFMaker) SavedGIF(string) bool { return false }

func (f *fakeGIFMaker) MadeGIF(_ context.Context, source model.Message) (model.Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	gif, ok := f.made[source.Media.ID]
	return gif, ok
}

func (f *fakeGIFMaker) SaveGIF(_ context.Context, m model.Message, unsave bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, m)
	return nil
}

func (f *fakeGIFMaker) SaveGIFFile(context.Context, model.Message, string, int, int, time.Duration) error {
	panic("no FFmpeg here")
}

func (f *fakeGIFMaker) ConvertGIFByBot(ctx context.Context, m model.Message, progress func(model.GIFProgress)) (model.Message, error) {
	f.mu.Lock()
	f.converted++
	f.mu.Unlock()
	progress(model.GIFProgress{Stage: model.GIFEncoding, Share: .5, Pass: 1})
	select {
	case gif := <-f.gifs:
		f.mu.Lock()
		f.made[m.Media.ID] = gif
		f.mu.Unlock()
		return gif, nil
	case <-ctx.Done():
		close(f.cancelled)
		return model.Message{}, ctx.Err()
	}
}

// The viewer's button saves a video as a GIF by the bot without FFmpeg;
// the dialog tells how far it has come, and its Cancel stops the bot's
// wait.
func TestMediaViewerSavesVideoAsGIF(t *testing.T) {
	h := newViewerHarness(t)
	maker := &fakeGIFMaker{gifs: make(chan model.Message, 1), cancelled: make(chan struct{}), made: map[string]model.Message{}}
	v := h.viewer
	v.gif.maker = maker
	clip := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 7}, Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "v7", MIMEType: "video/mp4", Width: 640, Height: 360, Duration: 5 * time.Second}}
	v.OpenAlone(1, clip)
	h.frame()
	if !v.canMakeGIF(clip) {
		t.Fatal("the video is not offered as a GIF")
	}
	protected := clip
	protected.NoForwards = true
	if v.canMakeGIF(protected) {
		t.Fatal("a protected video is offered as a GIF")
	}
	l := localization.For("en")

	v.gif.button.Click()
	h.until("the encoding", func() bool {
		return v.gif.work != nil && v.gif.work.state().Stage == model.GIFEncoding
	})
	if !v.gif.modal.Shown() || !v.gif.work.bot {
		t.Fatal("no dialog of the bot's conversion")
	}
	if p := v.gif.work.state(); gifStatus(p, l) != l.T("viewer.gif_encoding") || gifShare(p) != .5 {
		t.Fatalf("status %q, share %v", gifStatus(p, l), gifShare(p))
	}
	v.gif.cancel.click.Click()
	h.until("the cancel", func() bool { return v.gif.work == nil })
	select {
	case <-maker.cancelled:
	case <-time.After(time.Second):
		t.Fatal("Cancel did not stop the bot's wait")
	}
	if len(maker.saved) != 0 || v.toast.Text() != "" {
		t.Fatalf("cancelled, saved %v, told %q", maker.saved, v.toast.Text())
	}
	h.until("the dialog closed", func() bool { return !v.gif.modal.Shown() })

	maker.cancelled = make(chan struct{})
	gif := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif", Size: 1 << 20}}
	maker.gifs <- gif
	v.gif.button.Click()
	h.until("the GIF saved", func() bool { return v.toast.Text() == l.T("viewer.gif_saved") })
	if len(maker.saved) != 1 || maker.saved[0].Media.ID != "gif" {
		t.Fatalf("saved %v", maker.saved)
	}

	// The same video again saves the GIF it made, not one more by the bot.
	h.until("the dialog closed", func() bool { return !v.gif.modal.Shown() })
	v.toast = toast{}
	v.gif.button.Click()
	h.until("the GIF saved again", func() bool { return v.toast.Text() == l.T("viewer.gif_saved") })
	maker.mu.Lock()
	defer maker.mu.Unlock()
	if maker.converted != 2 || len(maker.saved) != 2 || maker.saved[1].Media.ID != "gif" {
		t.Fatalf("converted %d times, saved %v", maker.converted, maker.saved)
	}
}

// Each failure is told by its own text.
func TestGIFResultText(t *testing.T) {
	l := localization.For("en")
	for err, key := range map[error]string{
		model.ErrGIFTooLarge:  "viewer.gif_too_large",
		model.ErrGIFBotSilent: "viewer.gif_bot_silent",
		model.ErrGIFBotBusy:   "viewer.gif_bot_busy",
		model.ErrGIFBotFailed: "viewer.gif_bot_failed",
		nil:                   "viewer.gif_saved",
	} {
		if got := gifResultText(err, nil, l); got != l.T(key) || got == key {
			t.Errorf("%v: %q", err, got)
		}
	}
	if gifResultText(context.Canceled, nil, l) != "" || gifResultText(model.ErrGIFBotCancelled, nil, l) != "" {
		t.Error("a cancel is told")
	}
}
