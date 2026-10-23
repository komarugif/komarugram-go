// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"math"
	"sync"
	"time"

	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/wasmmodule"
	"komarugram/pkg/aac"
	"komarugram/pkg/audio"
	"komarugram/pkg/mp4"
	"komarugram/pkg/player"
	"komarugram/pkg/video"
)

// Where a video plays: in the external player, or in the viewer, its
// pictures and its sound decoded in the WASM sandboxes or by FFmpeg, as the
// settings choose (videoEngineOf).
const (
	videoExternal = "external"
	videoWASM     = "wasm"
	videoFFmpeg   = "ffmpeg"
)

// videoHD tells whether a video of width by height is HD at most, the
// largest the H.264 sandbox plays: 1280 by 720, either way up.
func videoHD(width, height int) bool {
	return width > 0 && height > 0 && max(width, height) <= 1280 && min(width, height) <= 720
}

// videoEngineOf is where m plays, as chosen: player is the video player's
// setting, "external" or else the client's own; sandbox is set when MP4
// animations play in the sandbox, as chosen or for want of an FFmpeg
// (chatmedia.Manager.SandboxGIFs). FFmpeg plays any video; the sandbox
// plays HD ones in MP4, and any other goes to the external player.
func videoEngineOf(m model.Message, player string, sandbox bool) string {
	if player == videoExternal || m.Kind != model.MessageVideo || m.Media == nil {
		return videoExternal
	}
	if !sandbox {
		return videoFFmpeg
	}
	if (m.Media.MIMEType == "video/mp4" || m.Media.MIMEType == "") && videoHD(m.Media.Width, m.Media.Height) {
		return videoWASM
	}
	return videoExternal
}

// videoPictures makes the pictures of a video as its sound comes to them.
type videoPictures interface {
	// run decodes the pictures as clock, where the sound is, comes to
	// their times, and calls shown for each picture to draw, until ctx is
	// done.
	run(ctx context.Context, clock func() time.Duration, shown func()) error
	// takeIn is the picture to draw in box, nil before the first.
	takeIn(now time.Time, box image.Point) image.Image
	duration() time.Duration
	close()
}

// ffmpegVideo is the picture of a video FFmpeg decodes, in a process of
// its own, from where the sound is, at the rate and size it was probed at,
// no larger than 1920 pixels across: the window scales it as it draws it.
type ffmpegVideo struct {
	url, ffmpeg string
	media       video.Media
	size        image.Point
	long        time.Duration

	mu sync.Mutex
	// frames decodes the pictures; the view's are given back to it.
	frames *video.Frames
	// picture is the picture to draw, taken the one the view drew last,
	// takenAt when.
	picture, taken *image.RGBA
	takenAt        time.Time
}

// ffmpegVideoSide bounds the pictures FFmpeg puts out: larger ones are
// scaled down to it.
const ffmpegVideoSide = 1920

func newFFmpegVideo(url, ffmpeg string, media video.Media, long time.Duration) *ffmpegVideo {
	size := image.Pt(media.Width, media.Height)
	if s := max(size.X, size.Y); s > ffmpegVideoSide {
		size = image.Pt(size.X*ffmpegVideoSide/s, size.Y*ffmpegVideoSide/s)
	}
	// FFmpeg's scaler wants even sides.
	size = image.Pt(max(2, size.X&^1), max(2, size.Y&^1))
	if media.Duration > 0 {
		long = media.Duration
	}
	return &ffmpegVideo{url: url, ffmpeg: ffmpeg, media: media, size: size, long: long}
}

func (f *ffmpegVideo) duration() time.Duration { return f.long }

func (f *ffmpegVideo) takeIn(now time.Time, _ image.Point) image.Image {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.takenAt = now
	if f.picture == nil {
		return nil
	}
	if f.taken != nil && f.taken != f.picture && f.frames != nil {
		f.frames.Release(f.taken)
	}
	f.taken = f.picture
	return f.picture
}

// show makes im the picture to draw, and gives back the one it replaces,
// unless the view may be drawing it.
func (f *ffmpegVideo) show(im *image.RGBA) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if old := f.picture; old != nil && old != f.taken && f.frames != nil {
		f.frames.Release(old)
	}
	f.picture = im
}

// drawn reports whether the view drew the pictures lately.
func (f *ffmpegVideo) drawn(now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.picture == nil || now.Sub(f.takenAt) < roundIdle
}

func (f *ffmpegVideo) run(ctx context.Context, clock func() time.Duration, shown func()) error {
	var frames *video.Frames
	defer func() {
		if frames != nil {
			frames.Close()
		}
	}()
	// at is the time of the picture shown last, below 0 for none; next is
	// one read ahead, to show at nextAt; ended is set once the pictures
	// ended.
	at, nextAt := time.Duration(-1), time.Duration(0)
	var next *image.RGBA
	ended := false
	frame := time.Duration(float64(time.Second) / f.media.FPS)
	for ctx.Err() == nil {
		pos := clock()
		if !f.drawn(time.Now()) {
			if !sleep(ctx, 100*time.Millisecond) {
				break
			}
			continue
		}
		// A start, or a move back, or far ahead, starts an FFmpeg where
		// the sound is.
		if frames == nil || at >= 0 && (pos < at-frame || pos > at+2*time.Second) || ended && pos < at-frame {
			if frames != nil {
				frames.Close()
			}
			var err error
			frames, err = video.NewFrames(f.url, f.ffmpeg, pos, image.Pt(f.media.Width, f.media.Height), f.size, f.media.FPS)
			if err != nil {
				return err
			}
			f.mu.Lock()
			f.frames = frames
			f.mu.Unlock()
			at, next, ended = -1, nil, false
		}
		decoded := false
		for !ended {
			if next == nil {
				im, t, err := frames.Next()
				if errors.Is(err, io.EOF) {
					ended = true
					break
				}
				if err != nil {
					return err
				}
				next, nextAt = im, t
			}
			// The first picture shows at once; the others at their times.
			if at >= 0 && nextAt > pos+frame/2 {
				break
			}
			f.show(next)
			at, next, decoded = nextAt, nil, true
		}
		if decoded {
			shown()
		}
		wait := 40 * time.Millisecond
		if next != nil {
			wait = min(max(nextAt-pos, 2*time.Millisecond), 40*time.Millisecond)
		}
		if !sleep(ctx, wait) {
			break
		}
	}
	return nil
}

func (f *ffmpegVideo) close() {
	f.mu.Lock()
	f.picture, f.taken, f.frames = nil, nil, nil
	f.mu.Unlock()
}

// ffmpegSound is the sound of a video FFmpeg decodes, in a process of its
// own started where it is to be read from: a move starts another one.
type ffmpegSound struct {
	url, ffmpeg string
	samples     int64
	pos         int64
	sound       *video.Sound
	buf         []byte
}

func (s *ffmpegSound) Read(pcm []audio.Frame) (int, error) {
	if s.pos >= s.samples {
		return 0, io.EOF
	}
	if s.sound == nil {
		sound, err := video.NewSound(s.url, s.ffmpeg, time.Duration(s.pos)*time.Second/audio.Rate)
		if err != nil {
			return 0, err
		}
		s.sound = sound
	}
	if cap(s.buf) < 4*len(pcm) {
		s.buf = make([]byte, 4*len(pcm))
	}
	n, err := io.ReadAtLeast(s.sound, s.buf[:4*len(pcm)], 4)
	frames := n / 4
	for i := range frames {
		pcm[i] = audio.Frame{int16(binary.LittleEndian.Uint16(s.buf[4*i:])), int16(binary.LittleEndian.Uint16(s.buf[4*i+2:]))}
	}
	s.pos += int64(frames)
	if n%4 != 0 {
		// A frame cut in two, at the very end.
		err = io.EOF
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	if frames > 0 && errors.Is(err, io.EOF) {
		// What was read plays; the end is told on the next read.
		s.samples = s.pos
		err = nil
	}
	return frames, err
}

func (s *ffmpegSound) SeekSample(pos int64) error {
	if s.sound != nil {
		s.sound.Close()
		s.sound = nil
	}
	s.pos = min(max(pos, 0), s.samples)
	return nil
}

func (s *ffmpegSound) Position() int64 { return s.pos }

func (s *ffmpegSound) close() {
	if s.sound != nil {
		s.sound.Close()
		s.sound = nil
	}
}

// videoState is what the viewer's player tells of its video.
type videoState struct {
	active, loading, playing, ended bool
	position, duration              time.Duration
	err                             error
}

// viewerVideo plays one video in the viewer, its picture drawn there and
// its sound put out: the sound is the clock the picture follows, as a video
// message's is, a silent one played over silence as long as it.
type viewerVideo struct {
	mu  sync.Mutex
	key model.MessageKey
	// engine is videoWASM or videoFFmpeg; external, when set, is told a
	// video neither takes, to open it in the external player from a time.
	engine   string
	loading  bool
	err      error
	playback audioPlayback
	stretch  *audio.Stretched
	source   *audioSource
	samples  int64
	pictures videoPictures
	cancel   context.CancelFunc
	work     *sync.WaitGroup
	release  func()
	// volume is how loud it plays, unmuted what muting brings back, speed
	// how fast; saveVolume keeps the volume chosen.
	volume, unmuted, speed float64
	saveVolume             func(float64)
	// external opens a video no decoder here takes in the external player
	// from a time; changed asks for a frame.
	external func(model.Message, time.Duration)
	changed  func()
	// play puts a source out: audio.Play, which tests replace.
	play func(audio.Source) (audioPlayback, error)
}

func newViewerVideo(changed func()) *viewerVideo {
	return &viewerVideo{volume: 1, unmuted: 1, speed: 1, changed: changed}
}

// open plays m of source from start with engine, FFmpeg at ffmpeg for
// videoFFmpeg ("" for the one found), whatever played before stopped.
func (v *viewerVideo) open(source model.ConversationStore, m model.Message, engine, ffmpeg string, start time.Duration) {
	v.mu.Lock()
	v.stopLocked()
	ctx, cancel := context.WithCancel(context.Background())
	v.key, v.engine, v.loading, v.err, v.cancel, v.work = m.Key, engine, true, nil, cancel, new(sync.WaitGroup)
	work := v.work
	work.Add(1)
	v.mu.Unlock()
	go func() {
		defer work.Done()
		defer crash.Recover("video", func(e *crash.Panic) { v.fail(m, e) })
		err := v.load(ctx, source, m, engine, ffmpeg, max(start, 0), work)
		if err != nil && ctx.Err() == nil {
			if errors.Is(err, errFormat) && v.external != nil {
				v.mu.Lock()
				mine := v.key == m.Key
				if mine {
					v.stopLocked()
				}
				v.mu.Unlock()
				if mine {
					v.external(m, start)
				}
			} else {
				v.fail(m, err)
			}
		}
		v.changed()
	}()
}

// load opens m's file, its pictures and its sound, and plays them.
func (v *viewerVideo) load(ctx context.Context, source model.ConversationStore, m model.Message, engine, ffmpeg string, start time.Duration, work *sync.WaitGroup) error {
	f, err := openFile(ctx, source, m)
	if err != nil {
		return err
	}
	var pictures videoPictures
	var sound audio.Source
	var samples int64
	releases := []func(){f.close}
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	switch engine {
	case videoWASM:
		round, err := openVideoFile(ctx, f)
		if err != nil {
			release()
			return err
		}
		pictures = round
		releases = append(releases, round.close)
		sound, samples, err = openVideoSound(ctx, f, round.duration())
		if err != nil {
			release()
			return err
		}
		if c, ok := sound.(interface{ close() }); ok {
			releases = append(releases, c.close)
		}
	default:
		// FFmpeg reads the file over a loopback stream, as the external
		// player does: it downloads as it is read.
		stream, err := player.Serve("video", f.r, f.size)
		if err != nil {
			release()
			return err
		}
		releases = append(releases, func() { _ = stream.Close() })
		media, err := video.ProbeMedia(ctx, stream.URL(), ffmpeg)
		if err != nil {
			release()
			return fmt.Errorf("%w: %w", errFormat, err)
		}
		long := m.Media.Duration
		if media.Duration > 0 {
			long = media.Duration
		}
		if long <= 0 {
			release()
			return fmt.Errorf("%w: no length", errFormat)
		}
		pictures = newFFmpegVideo(stream.URL(), ffmpeg, media, long)
		releases = append(releases, pictures.close)
		if media.Sound {
			s := &ffmpegSound{url: stream.URL(), ffmpeg: ffmpeg, samples: int64(long) * audio.Rate / int64(time.Second)}
			sound, samples = s, s.samples
			releases = append(releases, s.close)
		} else {
			s := silenceOf(long)
			sound, samples = s, s.samples
		}
	}
	if start > 0 {
		if err := sound.SeekSample(min(int64(start)*audio.Rate/int64(time.Second), samples)); err != nil {
			release()
			return err
		}
	}
	stretch := audio.Stretch(sound)
	src := &audioSource{reader: stretch}
	v.mu.Lock()
	play := v.play
	v.mu.Unlock()
	if play == nil {
		play = func(src audio.Source) (audioPlayback, error) {
			p, err := audio.Play(src)
			if err != nil {
				return nil, err
			}
			return p, nil
		}
	}
	playback, err := play(src)
	v.mu.Lock()
	if err != nil || ctx.Err() != nil {
		v.mu.Unlock()
		if playback != nil {
			playback.Close()
		}
		src.close()
		release()
		return err
	}
	v.loading, v.playback, v.source, v.release, v.samples = false, playback, src, release, samples
	v.stretch, v.pictures = stretch, pictures
	playback.SetVolume(v.volume)
	stretch.SetTempo(v.speed)
	v.mu.Unlock()
	clock := func() time.Duration {
		if playback.Ended() {
			return pictures.duration()
		}
		return time.Duration(playback.Position()) * time.Second / audio.Rate
	}
	work.Add(2)
	go func() {
		defer work.Done()
		defer crash.Recover("video pictures", nil)
		if err := pictures.run(ctx, clock, v.changed); err != nil && ctx.Err() == nil {
			v.fail(m, err)
		}
	}()
	go func() {
		defer crash.Recover("video end", nil)
		// The end, when it comes, is drawn.
		defer work.Done()
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		ended := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			if e := playback.Ended(); e != ended {
				ended = e
				v.changed()
			}
		}
	}()
	return nil
}

// openVideoSound opens the sound of the video in f, AAC decoded in its
// sandbox, or silence as long as the video when it has none. Other sound
// goes to the external player: errFormat.
func openVideoSound(ctx context.Context, f *audioFile, long time.Duration) (audio.Source, int64, error) {
	if _, err := mp4.DemuxAudioAt(f.r, f.size); errors.Is(err, mp4.ErrNoAudio) {
		s := silenceOf(long)
		return s, s.samples, nil
	}
	module, err := wasmmodule.AACDec.Load(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", errFormat, err)
	}
	rt, err := aac.NewRuntime(ctx, module)
	if err != nil {
		return nil, 0, err
	}
	d, err := rt.OpenAt(context.Background(), f.r, f.size)
	var src *audio.Resampled
	if err == nil {
		src, err = audio.Resample(d, d.Rate(), d.Frames())
	}
	if err != nil {
		rt.Close(context.Background())
		return nil, 0, fmt.Errorf("%w: %w", errFormat, err)
	}
	return &closingSource{Resampled: src, release: func() { rt.Close(context.Background()) }}, src.Samples(), nil
}

// closingSource is a sound whose decoder close frees.
type closingSource struct {
	*audio.Resampled
	release func()
}

func (s *closingSource) close() { s.release() }

// fail stops m, and keeps why, for the viewer to tell.
func (v *viewerVideo) fail(m model.Message, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key != m.Key {
		return
	}
	v.stopLocked()
	v.key, v.err = m.Key, err
	v.changed()
}

// takeErr is why the video stopped, once.
func (v *viewerVideo) takeErr() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	err := v.err
	v.err = nil
	if err != nil {
		v.key = model.MessageKey{}
	}
	return err
}

// stop ends what plays and gives back its memory and its processes.
func (v *viewerVideo) stop() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stopLocked()
}

func (v *viewerVideo) stopLocked() {
	if v.cancel != nil {
		v.cancel()
	}
	if v.playback != nil {
		v.playback.Close()
	}
	if release, source, work := v.release, v.source, v.work; release != nil {
		go func() {
			defer crash.Recover("video release", nil)
			source.close()
			if work != nil {
				work.Wait()
			}
			release()
		}()
	}
	v.key, v.loading, v.playback, v.release, v.source, v.work, v.samples, v.cancel = model.MessageKey{}, false, nil, nil, nil, nil, 0, nil
	v.stretch, v.pictures = nil, nil
}

// state is what the player tells of m.
func (v *viewerVideo) state(m model.Message) videoState {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key != m.Key || v.key == (model.MessageKey{}) {
		return videoState{}
	}
	s := videoState{active: true, loading: v.loading, err: v.err}
	if v.playback != nil && v.samples > 0 {
		pos := v.playback.Position()
		s.ended = v.playback.Ended()
		if s.ended {
			pos = v.samples
		}
		s.playing = v.playback.Playing() && !s.ended
		s.position = time.Duration(pos) * time.Second / audio.Rate
		s.duration = time.Duration(v.samples) * time.Second / audio.Rate
	}
	return s
}

// picture is the picture of m to draw in box, nil before its first.
func (v *viewerVideo) picture(m model.Message, now time.Time, box image.Point) image.Image {
	v.mu.Lock()
	pictures := v.pictures
	mine := v.key == m.Key
	v.mu.Unlock()
	if !mine || pictures == nil {
		return nil
	}
	return pictures.takeIn(now, box)
}

// toggle pauses m, or plays it on; an ended one plays from the start.
func (v *viewerVideo) toggle() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.playback == nil {
		return
	}
	switch {
	case v.playback.Ended():
		_ = v.playback.SeekSample(0)
		v.playback.Resume()
	case v.playback.Playing():
		v.playback.Pause()
	default:
		v.playback.Resume()
	}
}

// seek moves to at, and plays on from there, paused or not as it was.
func (v *viewerVideo) seek(at time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.playback == nil {
		return
	}
	ended := v.playback.Ended()
	_ = v.playback.SeekSample(min(max(int64(at)*audio.Rate/int64(time.Second), 0), v.samples))
	if ended {
		v.playback.Resume()
	}
}

// setSpeed plays it speed times as fast.
func (v *viewerVideo) setSpeed(speed float64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.speed = speed
	if v.stretch != nil {
		v.stretch.SetTempo(speed)
	}
}

// nextSpeed goes to the speed after the one it plays at.
func (v *viewerVideo) nextSpeed() {
	v.mu.Lock()
	next := audioSpeeds[0]
	for i, s := range audioSpeeds {
		if s == v.speed {
			next = audioSpeeds[(i+1)%len(audioSpeeds)]
		}
	}
	v.mu.Unlock()
	v.setSpeed(next)
}

// currentSpeed is the speed it plays at.
func (v *viewerVideo) currentSpeed() float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.speed
}

// loudness is the volume, from 0 to 1.
func (v *viewerVideo) loudness() float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.volume
}

// setVolume makes it as loud as volume, and keeps the choice with keep.
func (v *viewerVideo) setVolume(volume float64, keep bool) {
	volume = min(max(volume, 0), 1)
	save := func() func(float64) {
		// Deferred: the playback is the system's, and a panic there must
		// not leave the lock held for every frame after.
		v.mu.Lock()
		defer v.mu.Unlock()
		v.volume = volume
		if volume > 0 {
			v.unmuted = volume
		}
		if v.playback != nil {
			v.playback.SetVolume(volume)
		}
		return v.saveVolume
	}()
	if keep && save != nil {
		save(volume)
	}
}

// toggleMute silences it, or brings back the volume it had.
func (v *viewerVideo) toggleMute() {
	v.mu.Lock()
	volume := 0.0
	if v.volume == 0 {
		volume = v.unmuted
	}
	v.mu.Unlock()
	v.setVolume(volume, true)
}

// playVideo plays m from at, below 0 from the start: a video in the
// viewer, when the settings choose it, and anything else, a GIF too large
// to play in the chat say, in the external player.
func (p *chatPage) playVideo(gtx layout.Context, m model.Message, at time.Duration, l localization.Catalog) {
	if m.Kind == model.MessageVideo && p.openVideo != nil && p.videoEngine != nil && p.videoEngine(m) != videoExternal {
		p.openVideo(m, at)
		return
	}
	p.playFrom(gtx, m, at, p.reportMedia, l)
}

// videoEngine is where m plays, as the settings choose.
func (a *App) videoEngine(m model.Message) string {
	g := a.preferences.Global()
	sandbox := g.AnimationPlayer == "wasm" || video.ResolveFFmpeg(g.FFmpegPath) == ""
	return videoEngineOf(m, g.VideoPlayer, sandbox)
}

// configureViewer lets v play videos as the settings choose, at the volume
// the client's sound has, pausing the window's audio player as one starts.
func (a *App) configureViewer(v *mediaViewer) {
	v.videoEngine = a.videoEngine
	v.ffmpeg = func() string { return a.preferences.Global().FFmpegPath }
	if a.history != nil {
		audio := a.history.audio
		v.pauseAudio = audio.pause
	}
	v.video.setVolume(float64(a.preferences.Global().AudioVolume)/100, false)
	v.video.saveVolume = func(volume float64) {
		if err := a.preferences.SetAudioVolume(int(math.Round(volume * 100))); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
}
