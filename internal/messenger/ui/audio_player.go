// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/wasmmodule"
	"komarugram/pkg/aac"
	"komarugram/pkg/audio"
	"komarugram/pkg/drdec"
	"komarugram/pkg/mp4"
	"komarugram/pkg/opus"
	"komarugram/pkg/voice"

	"gio-mw/token"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// audioPlayer plays the voice messages, the video messages and the music of
// a window's chats in the client, one at a time, whatever chat is shown
// meanwhile, each decoder in a WebAssembly sandbox of its own: libopus
// (pkg/opus) for Opus, dr_libs (pkg/drdec) for MP3, FLAC and WAV, fdk-aac
// (pkg/aac, fetched) for M4A and the sound of video messages, whose
// pictures FFmpeg's H.264 decoder (pkg/h264, fetched) makes as the sound
// comes to them (roundVideo); pkg/audio puts the sound out. A file that
// downloads plays as it does: the decoders read it a range at a time, and
// the rest downloads ahead. A format none of them takes opens in the
// external player.
type audioPlayer struct {
	mu sync.Mutex
	// key is the message playing or loading; zero for none. msg is that
	// message, page the one it was played from, which tells its failures
	// and has the store, and list the chat, or the thread, it is shown in:
	// the voice message or the music after it there plays next.
	key     model.MessageKey
	msg     model.Message
	page    *chatPage
	list    int64
	loading bool
	// stretch changes the speed of what plays. speed is the one chosen for
	// what changes its speed, 1 when zero; saveSpeed keeps the choice.
	stretch   *audio.Stretched
	speed     float64
	saveSpeed func(float64)
	// volume is how loud what plays is, from 0 to 1, and unmuted what the
	// muted button brings back; saveVolume keeps the choice.
	volume, unmuted float64
	saveVolume      func(float64)
	// before and after tell whether a message of its kind precedes and
	// follows what plays, as of revision aroundRev of the history of
	// aroundKey.
	before, after bool
	aroundKey     model.MessageKey
	aroundRev     uint64
	// playback and source are the message's while it is loaded; they go
	// when another plays, or the chat changes, and release then frees the
	// decoder and the file under them.
	playback audioPlayback
	source   *audioSource
	release  func()
	// video makes the pictures of a video message that plays.
	video *roundVideo
	// work counts what else uses the decoder or the file: they are freed
	// once it is done.
	work    *sync.WaitGroup
	samples int64
	cancel  context.CancelFunc
	// waveforms are those worked out for voice messages sent without one,
	// by media ID. shown are the IDs whose waveform was sought as soon as
	// they were shown, each once; busy tells one is: one at a time.
	waveforms map[string][]byte
	shown     map[string]bool
	busy      bool
	// external is a message whose format no decoder here takes, for the
	// page to open in the external player on its next frame, from
	// externalAt (below 0, from the start).
	external   *model.Message
	externalAt time.Duration
	// play puts a source out: audio.Play, which tests replace.
	play func(audio.Source) (audioPlayback, error)
}

// audioPlayback is an *audio.Playback, or what a test gives instead.
type audioPlayback interface {
	Pause()
	Resume()
	Playing() bool
	Ended() bool
	Position() int64
	SeekSample(pos int64) error
	SetVolume(volume float64)
	Close()
}

// audioState is what the player tells of a message.
type audioState struct {
	active, loading, playing bool
	// progress is how much of the message was heard, from 0 to 1.
	progress float32
	elapsed  time.Duration
}

// audioFormat is how voice message, video message or music m is decoded in
// the client: "opus", "dr" for pkg/drdec, "aac", "round" for an MP4 video
// message, or "" when it is not.
func audioFormat(m model.Message) string {
	if m.Kind == model.MessageRound && m.Media != nil && (m.Media.MIMEType == "video/mp4" || m.Media.MIMEType == "") {
		return "round"
	}
	if m.Kind != model.MessageVoice && m.Kind != model.MessageMusic || m.Media == nil {
		return ""
	}
	mime := m.Media.MIMEType
	switch {
	case mime == "audio/ogg" || mime == "audio/opus" || mime == "" && m.Kind == model.MessageVoice:
		return "opus"
	case drdec.FormatOf(mime) != 0:
		return "dr"
	case mime == "audio/mp4" || mime == "audio/m4a" || mime == "audio/x-m4a":
		return "aac"
	}
	return ""
}

// internalAudio reports whether m plays in the client.
func internalAudio(m model.Message) bool { return audioFormat(m) != "" }

// errFormat marks a file no decoder here takes: it opens in the external
// player instead.
var errFormat = errors.New("audio: format not supported")

// audioFile is the file of a message as the player reads it.
type audioFile struct {
	r    io.ReaderAt
	size int64
	// data is the file when it is all there; nil while it downloads.
	data  []byte
	close func()
}

// openFile gives m's file: a stream that downloads as it is read, when the
// store has one and the file is not all there yet, or else the file.
func openFile(ctx context.Context, source model.ConversationStore, m model.Message) (*audioFile, error) {
	if s, ok := source.(interface {
		MediaStream(context.Context, model.Message) (io.ReaderAt, int64, func(), error)
	}); ok && m.Media.Size > 0 {
		r, size, closeStream, err := s.MediaStream(context.Background(), m)
		if err != nil {
			return nil, err
		}
		f := &audioFile{r: r, size: size, close: closeStream}
		if b, ok := r.(*bytes.Reader); ok {
			// Cached whole: no stream.
			f.data = make([]byte, b.Size())
			if _, err := b.ReadAt(f.data, 0); err != nil && !errors.Is(err, io.EOF) {
				f.data = nil
			}
		}
		return f, nil
	}
	data, err := source.Media(ctx, m)
	if err != nil {
		return nil, err
	}
	return fileOf(data), nil
}

// fileOf is data as an audioFile.
func fileOf(data []byte) *audioFile {
	return &audioFile{r: bytes.NewReader(data), size: int64(len(data)), data: data, close: func() {}}
}

// all is the whole file, downloaded if it is not yet.
func (f *audioFile) all() ([]byte, error) {
	if f.data != nil {
		return f.data, nil
	}
	data := make([]byte, f.size)
	if _, err := f.r.ReadAt(data, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return data, nil
}

// openAudio opens m's file, f, with a decoder of its own, and returns it as
// a source with its length and what frees the decoder. A file of a format
// the decoder does not take fails with errFormat.
func openAudio(ctx context.Context, m model.Message, f *audioFile) (audio.Source, int64, func(), error) {
	resampled := func(d interface {
		audio.Source
		Rate() int64
		Frames() int64
	}, err error, release func()) (audio.Source, int64, func(), error) {
		var src *audio.Resampled
		if err == nil {
			src, err = audio.Resample(d, d.Rate(), d.Frames())
		}
		if err != nil {
			release()
			return nil, 0, nil, fmt.Errorf("%w: %w", errFormat, err)
		}
		return src, src.Samples(), release, nil
	}
	switch audioFormat(m) {
	case "aac", "round":
		module, err := wasmmodule.AACDec.Load(ctx)
		if err != nil {
			// No decoder to fetch, offline say: the external player may
			// have one.
			return nil, 0, nil, fmt.Errorf("%w: %w", errFormat, err)
		}
		rt, err := aac.NewRuntime(ctx, module)
		if err != nil {
			return nil, 0, nil, err
		}
		release := func() { rt.Close(context.Background()) }
		// Reading goes on after ctx, which only bounds the opening.
		d, err := rt.OpenAt(context.Background(), f.r, f.size)
		return resampled(d, err, release)
	case "dr":
		rt, err := drdec.NewRuntime(ctx)
		if err != nil {
			return nil, 0, nil, err
		}
		release := func() { rt.Close(context.Background()) }
		// An MP3 still downloading takes its length from Telegram, not from
		// reading it through.
		var length time.Duration
		if f.data == nil {
			length = m.Media.Duration
		}
		d, err := rt.OpenAt(context.Background(), drdec.FormatOf(m.Media.MIMEType), f.r, f.size, length)
		return resampled(d, err, release)
	}
	// An OGG file is read whole: its last page tells its length, and voice
	// messages are small.
	data, err := f.all()
	if err != nil {
		return nil, 0, nil, err
	}
	stream, err := opus.Parse(data)
	if err != nil {
		// Vorbis, say.
		return nil, 0, nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	rt, err := opus.NewRuntime(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	release := func() { rt.Close(context.Background()) }
	decoder, err := rt.NewDecoder(ctx)
	var reader *opus.Reader
	if err == nil {
		reader, err = stream.NewReader(context.Background(), decoder)
	}
	if err != nil {
		release()
		return nil, 0, nil, err
	}
	return reader, stream.Samples(), release, nil
}

// state is what the player tells of m.
func (v *audioPlayer) state(m model.Message) audioState {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key != m.Key || v.key == (model.MessageKey{}) {
		return audioState{}
	}
	s := audioState{active: true, loading: v.loading}
	if v.playback != nil && v.samples > 0 {
		pos := v.playback.Position()
		if v.playback.Ended() {
			pos = v.samples
		}
		s.playing = v.playback.Playing()
		s.progress = min(1, float32(pos)/float32(v.samples))
		s.elapsed = time.Duration(pos) * time.Second / audio.Rate
	}
	return s
}

// waveform is m's, or the one worked out for it; nil if there is neither.
func (v *audioPlayer) waveform(m model.Message) []byte {
	if len(m.Media.Waveform) > 0 {
		return m.Media.Waveform
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.waveforms[m.Media.ID]
}

// roundPicture is the picture of video message m to draw, side pixels
// across, while m plays; nil otherwise, and before its first picture.
func (v *audioPlayer) roundPicture(m model.Message, now time.Time, side int) image.Image {
	v.mu.Lock()
	video := v.video
	playing := v.key == m.Key && v.key != (model.MessageKey{})
	v.mu.Unlock()
	if video == nil || !playing {
		return nil
	}
	return video.take(now, side)
}

// takeExternal is the message to open in the external player, once, and
// the time to open it at (below 0, the start).
func (v *audioPlayer) takeExternal() (*model.Message, time.Duration) {
	if v == nil {
		return nil, -1
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	m := v.external
	v.external = nil
	return m, v.externalAt
}

// audioSeek is where to move what plays, of samples in all: the sample
// to play from.
type audioSeek func(samples int64) int64

// fractionSeek moves to at, from 0 to 1, of what plays; nil below 0.
func fractionSeek(at float32) audioSeek {
	if at < 0 {
		return nil
	}
	return func(samples int64) int64 { return int64(at * float32(samples)) }
}

// timeSeek moves to at from the start, or to the end of what is shorter.
func timeSeek(at time.Duration) audioSeek {
	return func(samples int64) int64 {
		return min(samples, int64(at*audio.Rate/time.Second))
	}
}

// toggle plays m, or pauses it, or goes on with it. at, from 0 to 1, moves
// it there first; below 0, it plays from where it is.
func (v *audioPlayer) toggle(p *chatPage, m model.Message, at float32) {
	v.toggleIn(p, p.chat, m, at)
}

// playFrom plays m from at, a time from its start, as a timestamp names
// one: m goes on from there when it plays already.
func (v *audioPlayer) playFrom(p *chatPage, m model.Message, at time.Duration) {
	v.start(p, p.chat, m, timeSeek(at), at)
}

// toggleIn is toggle for a message shown in list, a chat or a thread of
// p's store, whatever p shows now.
func (v *audioPlayer) toggleIn(p *chatPage, list int64, m model.Message, at float32) {
	v.start(p, list, m, fractionSeek(at), -1)
}

// start is toggleIn moving to seek, unless it is nil; externalAt is the
// time the external player is to start at when no decoder here takes m.
func (v *audioPlayer) start(p *chatPage, list int64, m model.Message, seek audioSeek, externalAt time.Duration) {
	v.mu.Lock()
	if v.key == m.Key && v.key != (model.MessageKey{}) {
		defer v.mu.Unlock()
		if v.loading || v.playback == nil {
			return
		}
		if seek != nil {
			_ = v.playback.SeekSample(seek(v.samples))
			v.playback.Resume()
			return
		}
		if v.playback.Playing() {
			v.playback.Pause()
		} else {
			v.playback.Resume()
		}
		return
	}
	v.stopLocked()
	ctx, cancel := context.WithCancel(context.Background())
	v.key, v.loading, v.cancel, v.work = m.Key, true, cancel, new(sync.WaitGroup)
	v.msg, v.page, v.list, v.externalAt = m, p, list, externalAt
	work := v.work
	work.Add(1)
	v.mu.Unlock()
	if listenable(m) && m.MediaUnread && !m.Outgoing {
		// Played, it is listened to, as in Telegram Desktop.
		if r, ok := p.source.(model.ContentReader); ok {
			r.ReadContents(m)
		}
	}
	go func() {
		defer work.Done()
		defer crash.Recover("audio", func(e *crash.Panic) { v.fail(m, p, e) })
		err := v.load(ctx, p, m, seek, work)
		if err != nil && ctx.Err() == nil {
			v.fail(m, p, err)
		}
		p.invalidate()
	}()
}

// streamPart is how far ahead of what plays the file is downloaded, a part
// at a time: MediaStream's parts.
const streamPart = 128 << 10

// load opens m's file, and starts playing it; the rest of a file that
// downloads as it plays is downloaded ahead, counted in work.
func (v *audioPlayer) load(ctx context.Context, p *chatPage, m model.Message, seek audioSeek, work *sync.WaitGroup) error {
	f, err := openFile(ctx, p.source, m)
	if err != nil {
		return err
	}
	var video *roundVideo
	if m.Kind == model.MessageRound {
		// A video message is small, and its pictures are demuxed from the
		// whole file: it plays once it is all there. One that is not, a
		// video sent as one, goes to the external player.
		if f.size > model.MaxMediaBytes {
			f.close()
			return fmt.Errorf("%w: a video message of %d bytes", errFormat, f.size)
		}
		data, err := f.all()
		f.close()
		if err != nil {
			return err
		}
		f = fileOf(data)
		if video, err = openRound(ctx, data); err != nil {
			return err
		}
	}
	reader, samples, releaseDecoder, err := openAudio(ctx, m, f)
	if video != nil && errors.Is(err, mp4.ErrNoAudio) {
		// Silent, it plays on a clock of its own length.
		silent := silenceOf(video.duration())
		reader, samples, releaseDecoder, err = silent, silent.samples, func() {}, nil
	}
	if err != nil {
		f.close()
		if video != nil {
			video.close()
		}
		return err
	}
	release := func() {
		releaseDecoder()
		f.close()
		if video != nil {
			video.close()
		}
	}
	if seek != nil {
		if pos := seek(samples); pos > 0 {
			err = reader.SeekSample(pos)
		}
	}
	stretch := audio.Stretch(reader)
	source := &audioSource{reader: stretch}
	var playback audioPlayback
	if err == nil {
		v.mu.Lock()
		play := v.play
		v.mu.Unlock()
		if play == nil {
			play = func(src audio.Source) (audioPlayback, error) {
				p, err := audio.Play(src)
				if err != nil {
					// Not a nil *audio.Playback in an audioPlayback.
					return nil, err
				}
				return p, nil
			}
		}
		playback, err = play(source)
	}
	v.mu.Lock()
	if err != nil || ctx.Err() != nil || v.key != m.Key {
		v.mu.Unlock()
		if playback != nil {
			playback.Close()
		}
		source.close()
		release()
		return err
	}
	v.loading, v.playback, v.source, v.release, v.samples = false, playback, source, release, samples
	v.stretch, v.video = stretch, video
	playback.SetVolume(v.volume)
	if speedChanges(m) && v.speed > 0 {
		stretch.SetTempo(v.speed)
	}
	v.mu.Unlock()
	work.Add(1)
	go func() {
		defer work.Done()
		defer crash.Recover("audio playback", func(e *crash.Panic) { v.fail(m, p, e) })
		v.watch(ctx, m, playback)
	}()
	if video != nil {
		// release, which closes the decoder, waits for it.
		work.Add(1)
		go func() {
			defer work.Done()
			defer crash.Recover("video message", nil)
			clock := func() time.Duration {
				if playback.Ended() {
					return video.duration()
				}
				return time.Duration(playback.Position()) * time.Second / audio.Rate
			}
			if err := video.run(ctx, clock, p.invalidate); err != nil && ctx.Err() == nil {
				// The sound goes on without the pictures.
				p.reportMedia(err)
			}
		}()
	}
	if f.data == nil {
		// The rest downloads while it plays, a part at a time, so that
		// what plays next is there, and a move ahead finds it.
		work.Add(1)
		go func() {
			defer work.Done()
			defer crash.Recover("audio stream", nil)
			buf := make([]byte, streamPart)
			for off := int64(0); off < f.size && ctx.Err() == nil; off += streamPart {
				if _, err := f.r.ReadAt(buf[:min(int64(streamPart), f.size-off)], off); err != nil && !errors.Is(err, io.EOF) {
					return
				}
			}
		}()
	}
	if m.Kind == model.MessageVoice {
		// One that came without a waveform gets one, from a decoder of its
		// own, while it plays.
		v.workOutWaveform(ctx, p.source, m, f)
	}
	return nil
}

// workOutWaveform makes the waveform of voice message m of source from its
// file, unless it has one.
func (v *audioPlayer) workOutWaveform(ctx context.Context, source model.ConversationStore, m model.Message, f *audioFile) {
	v.mu.Lock()
	has := len(m.Media.Waveform) > 0 || v.waveforms[m.Media.ID] != nil
	v.mu.Unlock()
	if has {
		return
	}
	w, err := waveformOf(ctx, m, f)
	if err != nil {
		return
	}
	v.mu.Lock()
	if v.waveforms == nil {
		v.waveforms = map[string][]byte{}
	}
	v.waveforms[m.Media.ID] = w
	v.mu.Unlock()
	// A store that keeps it has it the next time, without the file.
	if k, ok := source.(model.WaveformKeeper); ok {
		k.KeepWaveform(m, w)
	}
}

// waveformOf decodes voice message m through and makes its waveform, as
// Telegram Desktop does for a voice message that came without one.
func waveformOf(ctx context.Context, m model.Message, f *audioFile) ([]byte, error) {
	reader, samples, release, err := openAudio(ctx, m, f)
	if err != nil {
		return nil, err
	}
	defer release()
	loudness := voice.NewLoudness(samples)
	pcm := make([]audio.Frame, audio.Rate/10)
	mono := make([]int16, len(pcm))
	for ctx.Err() == nil {
		n, err := reader.Read(pcm)
		for i, f := range pcm[:n] {
			mono[i] = f.Mono()
		}
		loudness.Add(mono[:n])
		if errors.Is(err, io.EOF) {
			return loudness.Waveform(), nil
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, ctx.Err()
}

// waveformMax is the largest voice message whose waveform is worked out as
// soon as it is shown: a longer one gets it when it is played.
const waveformMax = 3 << 20

// showWaveform works out, in the background, the waveform of voice message
// m shown without one, as Telegram Desktop does once it has the file: one
// at a time, each once.
func (v *audioPlayer) showWaveform(p *chatPage, m model.Message) {
	if m.Kind != model.MessageVoice || !internalAudio(m) || len(m.Media.Waveform) > 0 || m.Media.Size > waveformMax {
		return
	}
	v.mu.Lock()
	if v.busy || v.shown[m.Media.ID] {
		v.mu.Unlock()
		return
	}
	if v.shown == nil {
		v.shown = map[string]bool{}
	}
	v.busy, v.shown[m.Media.ID] = true, true
	v.mu.Unlock()
	// The worker takes a copy of its own: captured, m itself would be moved
	// to the heap on every call, every frame, for each voice message shown.
	voiceMessage := m
	go func() {
		defer func() {
			v.mu.Lock()
			v.busy = false
			v.mu.Unlock()
			p.invalidate()
		}()
		defer crash.Recover("voice waveform", nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		data, err := p.source.Media(ctx, voiceMessage)
		if err != nil {
			// Offline, say: it is tried again when played.
			return
		}
		v.workOutWaveform(ctx, p.source, voiceMessage, fileOf(data))
	}()
}

// fail lets m go, and tells why it did not play; one no decoder here takes
// goes to the external player instead.
func (v *audioPlayer) fail(m model.Message, p *chatPage, err error) {
	v.mu.Lock()
	if v.key == m.Key {
		v.stopLocked()
	}
	external := errors.Is(err, errFormat)
	if external {
		v.external = &m
	}
	v.mu.Unlock()
	if !external {
		p.reportMedia(err)
	}
	p.invalidate()
}

// pause pauses what plays, as a video that starts in the viewer does.
func (v *audioPlayer) pause() {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.playback != nil && v.playback.Playing() {
		v.playback.Pause()
	}
}

// stop ends what plays and gives back its memory.
func (v *audioPlayer) stop() {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stopLocked()
}

func (v *audioPlayer) stopLocked() {
	if v.cancel != nil {
		v.cancel()
	}
	if v.playback != nil {
		v.playback.Close()
	}
	if release, source, work := v.release, v.source, v.work; release != nil {
		// The output may be reading the source: its decoder is freed once
		// it is done, and the UI does not wait for that.
		go func() {
			defer crash.Recover("audio release", nil)
			source.close()
			if work != nil {
				work.Wait()
			}
			release()
		}()
	}
	v.key, v.loading, v.playback, v.release, v.source, v.work, v.samples, v.cancel = model.MessageKey{}, false, nil, nil, nil, nil, 0, nil
	v.msg, v.list, v.stretch, v.video = model.Message{}, 0, nil, nil
}

// audioSource is the reader of a voice message, as the output reads it
// from its own goroutine: once closed, it reads nothing more, so that the
// runtime under it can close.
type audioSource struct {
	mu     sync.Mutex
	reader audio.Source
	closed bool
}

func (s *audioSource) Read(pcm []audio.Frame) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.EOF
	}
	return s.reader.Read(pcm)
}

func (s *audioSource) SeekSample(pos int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return s.reader.SeekSample(pos)
}

func (s *audioSource) Position() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reader.Position()
}

// close waits for a read under way, and ends reading.
func (s *audioSource) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// audioRow is what the row of a voice message or music keeps between
// frames: where its waveform or bar was, and where a drag across it is.
type audioRow struct {
	button widget.Clickable
	// width is the waveform's or the bar's on the last frame; drag is where
	// a press on it is, from 0 to 1, below 0 when there is none.
	width   int
	drag    float32
	pressed bool
	// bars are the waveform's bars, worked out of waveform once rather than
	// in every frame.
	waveform []byte
	bars     []int
	// round is a video message's: where a press on it is.
	round roundSeek
}

// waveformBars returns the bars of waveform, kept from the last frame while
// it is the same.
func (r *audioRow) waveformBars(waveform []byte) []int {
	if r.bars == nil || !bytes.Equal(r.waveform, waveform) {
		r.waveform, r.bars = waveform, voice.Bars(waveform)
	}
	return r.bars
}

// update takes the clicks on the row's button and the presses on its
// waveform or bar: a click there, or a drag across, moves m there on
// release. It returns what the player tells of m, and the progress to draw:
// the drag's, while there is one.
func (vr *audioRow) update(gtx layout.Context, p *chatPage, m model.Message) (audioState, float32) {
	if !vr.pressed {
		vr.drag = -1
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: vr, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok || vr.width <= 0 {
			continue
		}
		at := max(0, min(1, e.Position.X/float32(vr.width)))
		switch e.Kind {
		case pointer.Press:
			vr.pressed, vr.drag = true, at
		case pointer.Drag:
			if vr.pressed {
				vr.drag = at
			}
		case pointer.Release:
			if vr.pressed {
				p.audio.toggle(p, m, at)
			}
			vr.pressed, vr.drag = false, -1
		case pointer.Cancel:
			vr.pressed, vr.drag = false, -1
		}
	}
	if vr.button.Clicked(gtx) {
		p.audio.toggle(p, m, -1)
	}
	state := p.audio.state(m)
	if state.playing || state.loading {
		// The time and the progress move while it plays.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(50 * time.Millisecond)})
	}
	progress := state.progress
	if vr.drag >= 0 {
		progress = vr.drag
	}
	return state, progress
}

// seekArea makes size, at the current origin, take the presses that move
// the row's audio.
func (vr *audioRow) seekArea(gtx layout.Context, size image.Point) {
	vr.width = size.X
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, vr)
	pointer.CursorPointer.Add(gtx.Ops)
	area.Pop()
}

// audioButton is the round button of an audio row: play, pause, or a ring
// while the file opens.
func (p *chatPage) audioButton(gtx layout.Context, vr *audioRow, state audioState, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	d := gtx.Dp(44)
	size := image.Pt(d, d)
	return vr.button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse{Max: size}.Op(gtx.Ops))
		gtx.Constraints = layout.Exact(size)
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if state.loading {
				return p.fileLoader.sized(gtx, l, 26)
			}
			icon := iconPlayFile
			if state.playing {
				icon = iconPauseFile
			}
			return exact(gtx, image.Pt(gtx.Dp(24), gtx.Dp(24)), func(gtx layout.Context) layout.Dimensions {
				return icon(gtx, sc.Primary.OnColor)
			})
		})
		return layout.Dimensions{Size: size}
	})
}

// audioTime is the time an audio row tells: its length, and how far it
// played once it did.
func audioTime(state audioState, length time.Duration) string {
	text := playTime(length)
	if state.active && !state.loading && (state.playing || state.elapsed > 0) {
		text = playTime(state.elapsed) + " / " + text
	}
	return text
}

// voiceLayout draws a voice message as Telegram Desktop does: a round
// button, the waveform, heard part in the primary color, and the time. A
// click on the waveform, or a drag across it, moves the message there.
func (p *chatPage) voiceLayout(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) layout.Dimensions {
	vr := &r.audio
	state, progress := vr.update(gtx, p, m)
	p.audio.showWaveform(p, m)
	sc := scheme(gtx)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.audioButton(gtx, vr, state, l) }),
		layout.Rigid(layout.Spacer{Width: 12}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(22))
					drawWaveform(gtx, vr.waveformBars(p.audio.waveform(m)), size, progress, sc.Primary.Color, sc.OutlineVariant)
					vr.seekArea(gtx, size)
					return layout.Dimensions{Size: size}
				}),
				vspace(4),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return label(gtx, audioTime(state, m.Media.Duration), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !m.MediaUnread {
								return layout.Dimensions{}
							}
							// Not listened to yet: a dot, as in Telegram Desktop.
							d := gtx.Dp(6)
							size := image.Pt(gtx.Dp(6)+d, d)
							paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse{Min: image.Pt(size.X-d, 0), Max: size}.Op(gtx.Ops))
							return layout.Dimensions{Size: size}
						}),
					)
				}),
			)
		}),
	)
}

// musicLayout draws music as Telegram Desktop does: a round button, the
// performer and the title, a bar of how much was heard, and the time. A
// click on the bar, or a drag across it, moves the music there.
func (p *chatPage) musicLayout(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) layout.Dimensions {
	vr := &r.audio
	state, progress := vr.update(gtx, p, m)
	sc := scheme(gtx)
	title := m.Media.Title
	if title == "" {
		title = m.Media.FileName
	}
	if title == "" {
		title = l.T("history.file")
	}
	performer := m.Media.Performer
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.audioButton(gtx, vr, state, l) }),
		layout.Rigid(layout.Spacer{Width: 12}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, title, token.TypestyleBodyLargeEmphasized, sc.Surface.OnColor, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if performer == "" {
						return layout.Dimensions{}
					}
					return label(gtx, performer, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
				}),
				vspace(6),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// The bar is thin; the area that moves it is taller.
					size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(16))
					drawBar(gtx, size, progress, state.active, sc.Primary.Color, sc.OutlineVariant)
					vr.seekArea(gtx, size)
					return layout.Dimensions{Size: size}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, audioTime(state, m.Media.Duration), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
				}),
			)
		}),
	)
}

// drawBar draws how much of music was heard: a thin track across size, the
// part before progress in heard, and a knob at progress while it plays.
func drawBar(gtx layout.Context, size image.Point, progress float32, knob bool, heard, rest token.MatColor) {
	h := gtx.Dp(3)
	y := (size.Y - h) / 2
	x := int(progress * float32(size.X))
	paint.FillShape(gtx.Ops, rest.AsNRGBA(), clip.UniformRRect(image.Rect(0, y, size.X, y+h), h/2).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, heard.AsNRGBA(), clip.UniformRRect(image.Rect(0, y, x, y+h), h/2).Op(gtx.Ops))
	if knob {
		d := gtx.Dp(10)
		c := image.Pt(min(max(x, d/2), size.X-d/2), size.Y/2)
		paint.FillShape(gtx.Ops, heard.AsNRGBA(), clip.Ellipse{Min: c.Sub(image.Pt(d/2, d/2)), Max: c.Add(image.Pt(d/2, d/2))}.Op(gtx.Ops))
	}
}

// drawWaveform draws bars, from 0 to 31, as rounded strokes across size,
// those before progress in heard and the rest in rest. Without bars, a flat
// line stands for the waveform.
func drawWaveform(gtx layout.Context, bars []int, size image.Point, progress float32, heard, rest token.MatColor) {
	stroke, gap := gtx.Dp(3), gtx.Dp(2)
	count := max(1, (size.X+gap)/(stroke+gap))
	for i := range count {
		value := 0
		if len(bars) > 0 {
			// Resample the bars to the strokes that fit, keeping peaks.
			from, to := i*len(bars)/count, max(i*len(bars)/count+1, (i+1)*len(bars)/count)
			for _, b := range bars[from:min(to, len(bars))] {
				value = max(value, b)
			}
		}
		h := max(stroke, size.Y*value/31)
		x := i * (stroke + gap)
		color := rest
		if float32(x+stroke/2) <= progress*float32(size.X) {
			color = heard
		}
		rect := image.Rect(x, (size.Y-h)/2, x+stroke, (size.Y+h)/2)
		paint.FillShape(gtx.Ops, color.AsNRGBA(), clip.UniformRRect(rect, stroke/2).Op(gtx.Ops))
	}
}

// playTime is a duration as a voice message tells it: 0:07, 1:23:45.
func playTime(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
