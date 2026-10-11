// SPDX-License-Identifier: Unlicense OR MIT

// Package voice records voice messages as Telegram sends them: the
// microphone is read by ffmpeg, as the external player plays media, into
// 16-bit mono PCM; ffmpeg encodes it to Opus in an OGG file; and Waveform
// packs the picture of its loudness that Telegram draws.
package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/program"
	"komarugram/pkg/video"
)

// Rate is the sample rate the microphone is recorded at.
const Rate = 48000

// MinDuration is the shortest recording worth sending, as Telegram
// Desktop's; MaxDuration the longest it records.
const (
	MinDuration = 200 * time.Millisecond
	MaxDuration = 100 * time.Minute
)

// ErrNoFFmpeg is returned when ffmpeg is not installed.
var ErrNoFFmpeg = errors.New("ffmpeg is not installed")

// FFmpeg finds ffmpeg: custom, the one the user set, or else the one on
// PATH.
func FFmpeg(custom string) (string, error) {
	path := video.ResolveFFmpeg(custom)
	if path == "" {
		return "", ErrNoFFmpeg
	}
	return path, nil
}

// Recorder records the microphone until it is stopped.
type Recorder struct {
	ffmpeg string
	ctx    context.Context
	cancel context.CancelFunc

	mu  sync.Mutex
	pcm []int16
	// level is the peak of the latest samples, from 0 to 1.
	level   float32
	stopped bool
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	err     error
	done    chan struct{}
}

// native records the microphone where ffmpeg cannot, until the recording
// stops, and reports whether any sound came; nil where ffmpeg does.
var native func(r *Recorder) (bool, error)

// Start starts recording: with the native recorder where there is one,
// else with ffmpeg, trying the system's inputs in turn
// until one gives sound. It does not wait: Failed tells when none does.
func Start(ctx context.Context, ffmpeg string) *Recorder {
	if native != nil {
		ctx, cancel := context.WithCancel(ctx)
		r := &Recorder{ffmpeg: ffmpeg, ctx: ctx, cancel: cancel, done: make(chan struct{})}
		go func() {
			defer close(r.done)
			_, err := native(r)
			r.finish(err)
		}()
		return r
	}
	return startWith(ctx, ffmpeg, func(ctx context.Context) ([][]string, error) { return inputs(ctx, ffmpeg) })
}

// startWith records from the first of the inputs list finds, the ffmpeg
// arguments that name an input, that gives sound.
func startWith(ctx context.Context, ffmpeg string, list func(context.Context) ([][]string, error)) *Recorder {
	ctx, cancel := context.WithCancel(ctx)
	r := &Recorder{ffmpeg: ffmpeg, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go r.run(list)
	return r
}

func (r *Recorder) run(list func(context.Context) ([][]string, error)) {
	defer close(r.done)
	inputs, err := list(r.ctx)
	if err != nil {
		r.finish(err)
		return
	}
	var errs []error
	for _, input := range inputs {
		got, err := r.record(input)
		if got || r.isStopped() {
			r.finish(err)
			return
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		errs = append(errs, errors.New("no audio input"))
	}
	r.finish(errors.Join(errs...))
}

func (r *Recorder) isStopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

func (r *Recorder) finish(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pcm) == 0 && err == nil {
		err = errors.New("no sound was recorded")
	}
	r.err = err
}

// record reads input until ffmpeg ends. It reports whether any sound came.
func (r *Recorder) record(input []string) (bool, error) {
	args := append([]string{"-hide_banner", "-loglevel", "error"}, input...)
	args = append(args, "-ac", "1", "-ar", fmt.Sprint(Rate), "-f", "s16le", "-flush_packets", "1", "-")
	cmd := program.CommandContext(r.ctx, r.ffmpeg, args...)
	program.Group(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return false, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return false, nil
	}
	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		return false, err
	}
	r.cmd, r.stdin = cmd, stdin
	r.mu.Unlock()
	got := false
	buf := make([]byte, 4800)
	var odd []byte
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			data := append(odd, buf[:n]...)
			samples := make([]int16, len(data)/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
			}
			odd = append([]byte(nil), data[2*len(samples):]...)
			got = got || len(samples) > 0
			r.add(samples)
		}
		if err != nil {
			break
		}
	}
	err = cmd.Wait()
	r.mu.Lock()
	r.cmd, r.stdin = nil, nil
	r.mu.Unlock()
	if err != nil && !got {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return false, fmt.Errorf("%s: %s", strings.Join(input, " "), msg)
		}
		return false, err
	}
	return got, nil
}

func (r *Recorder) add(samples []int16) {
	var peak int
	for _, s := range samples {
		peak = max(peak, abs(int(s)))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pcm)+len(samples) > int(MaxDuration/time.Second)*Rate {
		samples = samples[:max(0, int(MaxDuration/time.Second)*Rate-len(r.pcm))]
	}
	r.pcm = append(r.pcm, samples...)
	r.level = float32(peak) / 32768
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Level is how loud the latest sound was, from 0 to 1.
func (r *Recorder) Level() float32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.level
}

// Duration is how long the recording is so far.
func (r *Recorder) Duration() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return samplesDuration(len(r.pcm))
}

func samplesDuration(n int) time.Duration {
	return time.Duration(n) * time.Second / Rate
}

// Full reports whether the recording reached MaxDuration.
func (r *Recorder) Full() bool {
	return r.Duration() >= MaxDuration
}

// Failed is why recording stopped by itself, such as no microphone; nil
// while it records.
func (r *Recorder) Failed() error {
	select {
	case <-r.done:
	default:
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Stop ends the recording and returns its samples. ffmpeg is asked to
// quit, so the sound it holds comes out, and killed if it does not.
func (r *Recorder) Stop() ([]int16, error) {
	r.mu.Lock()
	r.stopped = true
	if r.stdin != nil {
		io.WriteString(r.stdin, "q")
		r.stdin.Close()
	}
	r.mu.Unlock()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		r.cancel()
		<-r.done
	}
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pcm) > 0 {
		return r.pcm, nil
	}
	return nil, r.err
}

// Cancel ends the recording and drops it.
func (r *Recorder) Cancel() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.cancel()
	<-r.done
	r.mu.Lock()
	r.pcm = nil
	r.mu.Unlock()
}

// Duration is how long pcm plays.
func Duration(pcm []int16) time.Duration { return samplesDuration(len(pcm)) }

// Encode writes pcm to path as Opus in OGG, the voice messages' format.
func Encode(ctx context.Context, ffmpeg string, pcm []int16, path string) error {
	data := make([]byte, 2*len(pcm))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(s))
	}
	cmd := program.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "s16le", "-ar", fmt.Sprint(Rate), "-ac", "1", "-i", "-",
		"-c:a", "libopus", "-b:a", "32k", "-application", "voip", "-f", "ogg", path)
	program.Group(cmd)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("encode voice: %s", msg)
		}
		return fmt.Errorf("encode voice: %w", err)
	}
	return nil
}

// WaveformBars is how many bars a voice message's waveform has.
const WaveformBars = 100

// Waveform is the loudness of pcm in WaveformBars bars of 5 bits, packed as
// Telegram keeps a voice message's waveform: bar i takes bits 5i to 5i+4,
// from the lowest bit of the first byte on. The loudest bar is 31.
func Waveform(pcm []int16) []byte {
	l := NewLoudness(int64(len(pcm)))
	l.Add(pcm)
	return l.Waveform()
}

// Loudness makes the waveform of a sound of total samples fed to it in
// order, a piece at a time, with the bars of Waveform: the whole sound
// need not be in memory.
type Loudness struct {
	total, seen int64
	bars        [WaveformBars]int
}

// NewLoudness starts the waveform of total samples.
func NewLoudness(total int64) *Loudness { return &Loudness{total: max(total, 1)} }

// Add takes the next samples; those past total are left out.
func (l *Loudness) Add(pcm []int16) {
	for _, s := range pcm {
		if l.seen >= l.total {
			return
		}
		// Bar i holds the samples from i*total/WaveformBars on.
		bar := ((l.seen+1)*WaveformBars+l.total-1)/l.total - 1
		l.bars[bar] = max(l.bars[bar], abs(int(s)))
		l.seen++
	}
}

// Waveform packs the bars as Telegram keeps them.
func (l *Loudness) Waveform() []byte {
	loudest := 0
	for _, bar := range l.bars {
		loudest = max(loudest, bar)
	}
	out := make([]byte, (WaveformBars*5+7)/8)
	if loudest == 0 {
		return out
	}
	for i, bar := range l.bars {
		value := bar * 31 / loudest
		bit := i * 5
		for b := range 5 {
			if value&(1<<b) != 0 {
				out[(bit+b)/8] |= 1 << ((bit + b) % 8)
			}
		}
	}
	return out
}

// Bars unpacks a waveform as Telegram keeps it into its bars, from 0 to 31:
// as many as its bits make, 100 for one of Waveform's.
func Bars(waveform []byte) []int {
	bars := make([]int, len(waveform)*8/5)
	for i := range bars {
		bit := i * 5
		for b := range 5 {
			if waveform[(bit+b)/8]&(1<<((bit+b)%8)) != 0 {
				bars[i] |= 1 << b
			}
		}
	}
	return bars
}
