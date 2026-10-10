// SPDX-License-Identifier: Unlicense OR MIT

// Package audio plays sound on the system's output, through oto: PulseAudio
// or PipeWire, else ALSA, on Linux, and WASAPI on Windows, all without cgo;
// on Haiku, the Media Kit, with a patch of oto (docs/PLATFORMS.md).
//
// The output is opened once, the first time something plays, and suspended
// while nothing does, so that an idle client keeps no audio stream running.
package audio

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

// Rate is the sample rate of the output and of every Source.
const Rate = 48000

// Source is stereo sound at Rate that can move to any of its frames.
type Source interface {
	// Read fills pcm with the next frames, and returns io.EOF at the end.
	Read(pcm []Frame) (int, error)
	// SeekSample moves to frame pos.
	SeekSample(pos int64) error
	// Position is the next frame Read puts out.
	Position() int64
}

var (
	outputOnce sync.Once
	output     *oto.Context
	outputErr  error

	// playing counts the playbacks not paused; the output is suspended
	// when there are none.
	playingMu sync.Mutex
	playing   int
)

// open opens the system's output, once.
func open() (*oto.Context, error) {
	outputOnce.Do(func() {
		ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
			SampleRate:   Rate,
			ChannelCount: 2,
			Format:       oto.FormatSignedInt16LE,
			// Short enough that a pause or a seek is heard at once.
			BufferSize: 80 * time.Millisecond,
		})
		if err != nil {
			outputErr = err
			return
		}
		<-ready
		if err := ctx.Err(); err != nil {
			outputErr = err
			return
		}
		output = ctx
		_ = output.Suspend()
	})
	return output, outputErr
}

// hold counts a playback that starts or stops playing, and resumes or
// suspends the output.
func hold(delta int) {
	playingMu.Lock()
	defer playingMu.Unlock()
	playing += delta
	if output == nil {
		return
	}
	if playing == 1 && delta > 0 {
		_ = output.Resume()
	} else if playing == 0 {
		_ = output.Suspend()
	}
}

// Playback is a Source playing on the output.
type Playback struct {
	player *oto.Player
	src    *stereo
	// mu guards paused and closed.
	mu             sync.Mutex
	paused, closed bool
}

// Play starts playing src from where it is.
func Play(src Source) (*Playback, error) {
	ctx, err := open()
	if err != nil {
		return nil, err
	}
	s := newStereo(src)
	p := &Playback{player: ctx.NewPlayer(s), src: s}
	hold(1)
	p.player.Play()
	return p, nil
}

// sync notices the sound ended: it is paused then, and holds the output no
// longer. p.mu is held.
func (p *Playback) sync() {
	if !p.paused && !p.closed && !p.player.IsPlaying() {
		p.paused = true
		hold(-1)
		p.src.mu.Lock()
		p.src.ended = true
		p.src.mu.Unlock()
	}
}

// Pause stops the sound where it is; Resume goes on from there.
func (p *Playback) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	if p.paused || p.closed {
		return
	}
	p.player.Pause()
	p.paused = true
	hold(-1)
}

// Resume plays again after Pause, or from the start after the end.
func (p *Playback) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	if !p.paused || p.closed {
		return
	}
	if p.Ended() {
		_, _ = p.player.Seek(0, io.SeekStart)
	}
	p.paused = false
	hold(1)
	p.player.Play()
}

// Playing reports whether the sound is playing: not paused, not ended.
func (p *Playback) Playing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	return !p.paused && !p.closed
}

// Ended reports whether the sound played to its end, and was not moved
// since.
func (p *Playback) Ended() bool {
	p.src.mu.Lock()
	defer p.src.mu.Unlock()
	return p.src.ended
}

// Position is the sample being heard.
func (p *Playback) Position() int64 {
	buffered := int64(p.player.BufferedSize() / 4)
	return max(0, p.src.pump.position()-buffered)
}

// SeekSample moves the sound to sample pos, playing or paused as it was.
func (p *Playback) SeekSample(pos int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("audio: playback closed")
	}
	_, err := p.player.Seek(pos*4, io.SeekStart)
	return err
}

// SetVolume sets how loud the sound is, from 0, silent, to 1, as it is.
func (p *Playback) SetVolume(volume float64) {
	p.player.SetVolume(min(max(volume, 0), 1))
}

// Close stops the sound for good.
func (p *Playback) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	if p.closed {
		return
	}
	if !p.paused {
		hold(-1)
	}
	p.closed = true
	p.src.pump.close()
	_ = p.player.Close()
}

// stereo turns a Source, read ahead by a pump, into the 16-bit stereo bytes
// oto reads.
type stereo struct {
	pump *pump
	// mu guards mono and ended.
	mu     sync.Mutex
	frames []Frame
	ended  bool
}

func newStereo(src Source) *stereo { return &stereo{pump: newPump(src)} }

func (s *stereo) Read(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := len(b) / 4
	if cap(s.frames) < want {
		s.frames = make([]Frame, want)
	}
	n, err := s.pump.read(s.frames[:want])
	for i, f := range s.frames[:n] {
		binary.LittleEndian.PutUint16(b[4*i:], uint16(f[0]))
		binary.LittleEndian.PutUint16(b[4*i+2:], uint16(f[1]))
	}
	return 4 * n, err
}

// Seek moves to a byte offset from the start, the only whence oto uses. It
// does not wait for the source to get there.
func (s *stereo) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekStart {
		return 0, errors.New("audio: seek from the start only")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = false
	s.pump.seek(offset / 4)
	return offset, nil
}
