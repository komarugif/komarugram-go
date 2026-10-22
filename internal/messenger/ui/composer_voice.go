// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"time"

	"gio-mw/token"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/voice"
)

// voiceRecorder is a recording of the microphone, a voice.Recorder.
type voiceRecorder interface {
	Level() float32
	Duration() time.Duration
	Full() bool
	Failed() error
	Stop() ([]int16, error)
	Cancel()
}

// voiceTools record the microphone and encode what it recorded, with the
// FFmpeg the user set or else the one on PATH; without one, they choose an
// audio file to send instead. Tests replace them.
type voiceTools struct {
	// record fails with voice.ErrNoFFmpeg when there is no FFmpeg.
	record func(ctx context.Context, ffmpeg string) (voiceRecorder, error)
	encode func(ctx context.Context, ffmpeg string, pcm []int16, path string) error
	// choose asks for an audio file in the system's chooser.
	choose func(ctx context.Context, name string) fileChoice
}

// ffmpegVoice records and encodes with ffmpeg.
var ffmpegVoice = voiceTools{
	record: func(ctx context.Context, custom string) (voiceRecorder, error) {
		ffmpeg, err := voice.FFmpeg(custom)
		if err != nil {
			return nil, err
		}
		return voice.Start(ctx, ffmpeg), nil
	},
	encode: func(ctx context.Context, custom string, pcm []int16, path string) error {
		ffmpeg, err := voice.FFmpeg(custom)
		if err != nil {
			return err
		}
		return voice.Encode(ctx, ffmpeg, pcm, path)
	},
	choose: func(ctx context.Context, name string) fileChoice {
		return chooseFile(ctx, &fileFilter{name: name, extensions: voice.FileExtensions})
	},
}

// voicePick is an audio file chosen to be sent as a voice message, or why
// it cannot be.
type voicePick struct {
	chat int64
	path string
	note model.VoiceNote
	err  error
}

// voiceRecording is a voice message being recorded, as Telegram Desktop's
// locked recording: the composer shows its time and loudness, the cross
// drops it and Send sends it.
type voiceRecording struct {
	rec  voiceRecorder
	chat int64
	// levels are the loudness of the latest moments, oldest first, and
	// sampled when the last was taken.
	levels  []float32
	sampled time.Time
}

// voiceResult is a recording encoded for sending, or why it was not.
type voiceResult struct {
	chat int64
	path string
	note model.VoiceNote
	err  error
}

// canRecord reports whether the composer offers to record: its text is
// empty and nothing is on its way.
func (c *messageComposer) canRecord(d *messageDraft) bool {
	return c.permissions(c.chat).Allows(model.SendVoice) && c.voice.record != nil && c.source != nil && c.recording == nil && !d.sending && d.pending == nil && d.editor.Text() == ""
}

// ffmpegPath is the FFmpeg the user set, or "" for the one on PATH.
func (c *messageComposer) ffmpegPath() string {
	if c.ffmpeg == nil {
		return ""
	}
	return c.ffmpeg()
}

// startRecording starts recording a voice message for the open chat.
// Without an FFmpeg, it asks for an audio file to send instead.
func (c *messageComposer) startRecording(l localization.Catalog) {
	if !c.permissions(c.chat).Allows(model.SendVoice) {
		return
	}
	d := c.draft(c.chat)
	rec, err := c.voice.record(c.ctx, c.ffmpegPath())
	if errors.Is(err, voice.ErrNoFFmpeg) {
		c.chooseVoiceFile(l)
		return
	}
	if err != nil {
		log.Printf("record voice: %v", err)
		d.err = errors.New(l.T("record.problem"))
		return
	}
	d.err = nil
	c.pickerOpen, c.attachOpen, c.form = false, false, 0
	c.recording = &voiceRecording{rec: rec, chat: c.chat}
}

// chooseVoiceFile asks for an audio file and sends it as a voice message,
// its duration read from its headers. It has no waveform: Telegram Desktop
// draws one itself once it has the file.
func (c *messageComposer) chooseVoiceFile(l localization.Catalog) {
	if c.choosing {
		return
	}
	c.choosing = true
	ctx, chat, choose := c.ctx, c.chat, c.voice.choose
	name, noFFmpeg, badFile := l.T("record.audio_files"), l.T("record.no_ffmpeg"), l.T("record.bad_file")
	go func() {
		defer crash.Recover("choose voice file", func(e *crash.Panic) {
			select {
			case c.voicePicks <- voicePick{chat: chat, err: e}:
				c.invalidate()
			case <-ctx.Done():
			}
		})
		f := choose(ctx, name)
		pick := voicePick{chat: chat, path: f.path}
		switch {
		case errors.Is(f.err, errNoChooser):
			pick.err = errors.New(noFFmpeg)
		case f.err != nil:
			pick.err = f.err
		case f.path != "":
			d, err := voice.FileDuration(f.path)
			if err != nil {
				log.Printf("voice file: %v", err)
				pick.err = errors.New(badFile)
			}
			pick.note = model.VoiceNote{Duration: d}
		}
		select {
		case c.voicePicks <- pick:
			c.invalidate()
		case <-ctx.Done():
		}
	}()
}

// cancelRecording drops the recording.
func (c *messageComposer) cancelRecording() {
	if r := c.recording; r != nil {
		c.recording = nil
		go r.rec.Cancel()
	}
}

// finishRecording stops the recording and sends it once it is encoded.
// Shorter than voice.MinDuration, it is dropped, as in Telegram Desktop.
func (c *messageComposer) finishRecording() {
	r := c.recording
	if r == nil {
		return
	}
	c.recording = nil
	ctx, ffmpeg := c.ctx, c.ffmpegPath()
	encode := func(ctx context.Context, pcm []int16, path string) error {
		return c.voice.encode(ctx, ffmpeg, pcm, path)
	}
	go func() {
		defer crash.Recover("voice message", func(e *crash.Panic) {
			select {
			case c.voiceResults <- voiceResult{chat: r.chat, err: e}:
				c.invalidate()
			case <-ctx.Done():
			}
		})
		res := voiceResult{chat: r.chat}
		pcm, err := r.rec.Stop()
		switch {
		case err != nil:
			res.err = err
		case voice.Duration(pcm) < voice.MinDuration:
		default:
			res.path, res.err = encodeVoice(ctx, encode, pcm)
			res.note = model.VoiceNote{Duration: voice.Duration(pcm), Waveform: voice.Waveform(pcm)}
		}
		select {
		case c.voiceResults <- res:
			c.invalidate()
		case <-ctx.Done():
			if res.path != "" {
				os.Remove(res.path)
			}
		}
	}()
}

// encodeVoice writes pcm to a temporary OGG file, removed once it is sent.
func encodeVoice(ctx context.Context, encode func(context.Context, []int16, string) error, pcm []int16) (string, error) {
	f, err := os.CreateTemp("", "komarugram-voice-*.ogg")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := encode(ctx, pcm, path); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// updateRecording follows the recording: it ends when the chat changes,
// the microphone fails or the time is up, and the cross and Escape drop it.
func (c *messageComposer) updateRecording(gtx layout.Context, l localization.Catalog) {
	if c.micClick.Clicked(gtx) && c.canRecord(c.draft(c.chat)) {
		c.startRecording(l)
		if c.recording != nil {
			// Nothing is typed while it records, and Escape reaches it
			// rather than the field.
			gtx.Execute(key.FocusCmd{})
		}
	}
	r := c.recording
	if r == nil {
		return
	}
	defer func() {
		if c.recording == nil && r.chat == c.chat {
			gtx.Execute(key.FocusCmd{Tag: &c.draft(c.chat).editor})
		}
	}()
	if r.chat != c.chat {
		c.cancelRecording()
		return
	}
	if err := r.rec.Failed(); err != nil {
		log.Printf("record voice: %v", err)
		c.recording = nil
		c.draft(r.chat).err = errors.New(l.T("record.problem"))
		return
	}
	if c.voiceCancel.Clicked(gtx) {
		c.cancelRecording()
		return
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			c.cancelRecording()
			return
		}
	}
	if c.send.Clicked(gtx) || r.rec.Full() {
		c.finishRecording()
		return
	}
	// The bars move on every 50 ms.
	if gtx.Now.Sub(r.sampled) >= 50*time.Millisecond {
		r.sampled = gtx.Now
		r.levels = append(r.levels, r.rec.Level())
		if len(r.levels) > 200 {
			r.levels = r.levels[len(r.levels)-200:]
		}
	}
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(50 * time.Millisecond)})
}

// voiceTime is how long a recording is, as Telegram Desktop shows it:
// minutes, seconds and tenths.
func voiceTime(d time.Duration) string {
	tenths := int(d / (100 * time.Millisecond))
	return fmt.Sprintf("%d:%02d,%d", tenths/600, tenths/10%60, tenths%10)
}

// layoutRecording draws the recording in the composer's middle: a red dot,
// its time, and bars of its loudness.
func (c *messageComposer) layoutRecording(gtx layout.Context) layout.Dimensions {
	r := c.recording
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	dot := gtx.Dp(10)
	// The dot fades in and out each second while it records.
	phase := float32(gtx.Now.UnixMilli()%1000) / 1000
	alpha := 0.4 + 0.6*(1-2*min(phase, 1-phase))
	offset(gtx, image.Pt(0, (size.Y-dot)/2), func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, sc.Error.Color.SetOpacity(token.OpacityLevel(alpha)), image.Pt(dot, dot), dot/2)
		return layout.Dimensions{Size: image.Pt(dot, dot)}
	})
	x := dot + gtx.Dp(10)
	var timeWidth int
	offset(gtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Pt(0, size.Y)
		return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			dims := label(gtx, voiceTime(r.rec.Duration()), token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
			timeWidth = dims.Size.X
			return dims
		})
	})
	x += timeWidth + gtx.Dp(16)
	// The bars, newest at the end, as many as fit.
	bar, gap := gtx.Dp(3), gtx.Dp(2)
	height := size.Y - gtx.Dp(24)
	n := max(0, (size.X-x)/(bar+gap))
	levels := r.levels[max(0, len(r.levels)-n):]
	for i, level := range levels {
		h := max(bar, int(float32(height)*min(1, level*1.5)))
		offset(gtx, image.Pt(x+i*(bar+gap), (size.Y-h)/2), func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, sc.Primary.Color, image.Pt(bar, h), bar/2)
			return layout.Dimensions{Size: image.Pt(bar, h)}
		})
	}
	return layout.Dimensions{Size: size}
}
