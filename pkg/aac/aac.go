// SPDX-License-Identifier: Unlicense OR MIT

// Package aac decodes the AAC sound of M4A files — voice messages sent from
// a file, and music — without cgo and without an FFmpeg.
//
// The decoder is the Fraunhofer FDK AAC Codec Library for Android, compiled
// to WebAssembly and executed by wazero, as the vp9 package does with
// libvpx: files arrive from strangers, and one that makes the decoder fail
// takes down nothing but its own instance. The module is not in this
// package: its license grants no patent rights, and it is built and
// published, with its sources, by https://github.com/komarugif/fdk-aac-wasm
// for the caller to hand to NewRuntime. The container is parsed in Go by
// the mp4 package, which hands over only the access units.
package aac

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"komarugram/pkg/audio"
	"komarugram/pkg/mp4"
	"komarugram/pkg/sandbox"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// maxOut is the most samples one access unit decodes to, AACW_MAX in
// aacshim.cpp: 2048 frames of 8 channels.
const maxOut = 2048 * 8

// preRoll is how many access units are decoded before the one a seek
// lands in: SBR and the transform lean on the unit before.
const preRoll = 2

// DefaultLimits suit a voice message or a song: a decoder keeps a few
// hundred KiB, and a unit takes well under a millisecond.
var DefaultLimits = sandbox.Limits{Memory: 16 << 20, Slow: 250 * time.Millisecond}

// Runtime compiles the decoder once; each file then gets its own instance.
type Runtime struct {
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
}

// NewRuntime compiles module, an aacdec.wasm, under DefaultLimits.
func NewRuntime(ctx context.Context, module []byte) (*Runtime, error) {
	rt, err := sandbox.NewRuntime(ctx, DefaultLimits)
	if err != nil {
		return nil, err
	}
	// fdk-aac's libc wants the WASI imports. It gets no preopened
	// directories, no arguments and no environment.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt.Wazero()); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wasi: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, module)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile fdk-aac: %w", err)
	}
	return &Runtime{sandbox: rt, compiled: compiled}, nil
}

// Close tears down every decoder created by this runtime.
func (r *Runtime) Close(ctx context.Context) error { return r.sandbox.Close(ctx) }

// Decoder is the sound track of one M4A file in its own sandbox, read as
// stereo frames at the rate the decoder puts out: see audio.Resample.
type Decoder struct {
	ctx       context.Context
	runtime   *Runtime
	module    api.Module
	functions map[string]api.Function
	handle    uint64
	in, out   uint64
	track     *mp4.Audio
	channels  int
	rate      int64
	// starts[i] is where unit i starts, in samples at rate.
	starts []int64
	// pos is the next sample Read puts out; next, the unit decoded next;
	// skip, how many of the samples to come fall before pos.
	pos, skip int64
	next      int
	ready     []audio.Frame
	pcm       []audio.Frame
	// unit is where access units are read into, from the file.
	unit []byte
}

// Open decodes the sound track of the M4A file in data. Reads go on under
// ctx.
func (r *Runtime) Open(ctx context.Context, data []byte) (*Decoder, error) {
	return r.OpenAt(ctx, bytes.NewReader(data), int64(len(data)))
}

// OpenAt decodes the sound track of the M4A file of size bytes that file
// reads: its access units are read as they play, so that a file that
// downloads as it plays need not be all there. The time file takes to
// read is not the decoder's, which the sandbox's limits bound.
func (r *Runtime) OpenAt(ctx context.Context, file io.ReaderAt, size int64) (*Decoder, error) {
	track, err := mp4.DemuxAudioAt(file, size)
	if err != nil {
		return nil, err
	}
	module, err := r.sandbox.Instantiate(ctx, r.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate fdk-aac: %w", err)
	}
	d := &Decoder{ctx: ctx, runtime: r, module: module, track: track, pcm: make([]audio.Frame, maxOut)}
	if err := d.open(); err != nil {
		_ = module.Close(ctx)
		return nil, err
	}
	return d, nil
}

func (d *Decoder) open() error {
	maxUnit := 0
	for _, u := range d.track.Units {
		maxUnit = max(maxUnit, int(u.Size))
	}
	if maxUnit == 0 || maxUnit > 1<<20 {
		return fmt.Errorf("aac: access unit of %d bytes", maxUnit)
	}
	var err error
	if d.in, err = d.call("aacw_alloc", uint64(max(maxUnit, len(d.track.Config)))); err != nil {
		return err
	}
	if d.out, err = d.call("aacw_alloc", 2*maxOut); err != nil {
		return err
	}
	if d.in == 0 || d.out == 0 || !d.module.Memory().Write(uint32(d.in), d.track.Config) {
		return errors.New("aac: sandbox out of memory")
	}
	if d.handle, err = d.call("aacw_open", d.in, uint64(len(d.track.Config))); err != nil {
		return err
	}
	if d.handle == 0 {
		return errors.New("aac: the decoder does not take this configuration")
	}
	// What it puts out is known once a unit decoded: SBR doubles the rate
	// the configuration states.
	if _, err := d.decode(0); err != nil {
		return err
	}
	rate, err := d.call("aacw_rate", d.handle)
	if err != nil {
		return err
	}
	channels, err := d.call("aacw_channels", d.handle)
	if err != nil {
		return err
	}
	d.rate, d.channels = int64(int32(rate)), int(int32(channels))
	if d.rate <= 0 || d.rate > 192000 || d.channels < 1 || d.channels > 8 {
		return fmt.Errorf("aac: %d channels at %d Hz", d.channels, d.rate)
	}
	scale := float64(d.rate) / float64(d.track.Timescale)
	d.starts = make([]int64, len(d.track.Starts))
	for i, s := range d.track.Starts {
		d.starts[i] = int64(float64(s) * scale)
	}
	return d.SeekSample(0)
}

// Close ends the decoder and its sandbox.
func (d *Decoder) Close() error { return d.module.Close(context.Background()) }

// Rate is the rate of the samples Read puts out.
func (d *Decoder) Rate() int64 { return d.rate }

// Frames is how many samples the track plays, at Rate.
func (d *Decoder) Frames() int64 { return d.starts[len(d.starts)-1] }

// Position is the next sample Read puts out.
func (d *Decoder) Position() int64 { return d.pos }

// SeekSample moves to sample pos, decoding from a few units before it.
func (d *Decoder) SeekSample(pos int64) error {
	pos = max(0, min(pos, d.Frames()))
	units := len(d.track.Units)
	k := sort.Search(units, func(i int) bool { return d.starts[i+1] > pos })
	k = max(0, min(k, units-1)-preRoll)
	if _, err := d.call("aacw_flush", d.handle); err != nil {
		return err
	}
	d.pos, d.next, d.skip, d.ready = pos, k, pos-d.starts[k], nil
	return nil
}

// Read fills out with the next frames, folded to stereo, and returns
// io.EOF at the end of the track.
func (d *Decoder) Read(out []audio.Frame) (int, error) {
	n := 0
	for n < len(out) && d.pos < d.Frames() {
		if len(d.ready) == 0 {
			if d.next >= len(d.track.Units) {
				break
			}
			frames, err := d.decode(d.next)
			if err != nil {
				return n, err
			}
			d.next++
			drop := min(d.skip, int64(frames))
			d.skip -= drop
			d.ready = d.pcm[drop:frames]
			continue
		}
		m := copy(out[n:], d.ready[:min(int64(len(d.ready)), d.Frames()-d.pos)])
		d.ready = d.ready[m:]
		d.pos += int64(m)
		n += m
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// decode decodes unit i into stereo and returns its frames. A unit the
// decoder cannot take, a broken one, decodes to silence of its length, as
// players conceal it.
func (d *Decoder) decode(i int) (int, error) {
	unit, err := d.track.Unit(i, d.unit)
	if err != nil {
		return 0, err
	}
	d.unit = unit
	started := time.Now()
	if !d.module.Memory().Write(uint32(d.in), unit) {
		return 0, errors.New("aac: unit does not fit in sandbox memory")
	}
	res, err := d.call("aacw_decode", d.handle, d.in, uint64(len(unit)), d.out)
	if err != nil {
		return 0, err
	}
	if err := d.runtime.sandbox.Check(d.ctx, d.module, started); err != nil {
		return 0, fmt.Errorf("aac: %w", err)
	}
	total := int(int32(res))
	channels := max(1, d.channels)
	if total <= 0 || total > maxOut {
		// Concealed: silence as long as the unit.
		frames := 1024
		if i+1 < len(d.starts) && d.starts != nil {
			frames = int(min(int64(maxOut), max(0, d.starts[i+1]-d.starts[i])))
		}
		clear(d.pcm[:frames])
		return frames, nil
	}
	if d.channels == 0 {
		// Before the first unit tells the layout, read it as it came.
		ch, err := d.call("aacw_channels", d.handle)
		if err != nil {
			return 0, err
		}
		channels = max(1, int(int32(ch)))
	}
	frames := total / channels
	raw, ok := d.module.Memory().Read(uint32(d.out), uint32(2*total))
	if !ok {
		return 0, errors.New("aac: samples lie outside sandbox memory")
	}
	audio.Downmix(d.pcm[:frames], raw, channels)
	return frames, nil
}

func (d *Decoder) call(name string, args ...uint64) (uint64, error) {
	fn := d.functions[name]
	if fn == nil {
		fn = d.module.ExportedFunction(name)
		if fn == nil {
			return 0, fmt.Errorf("aac: missing export %s", name)
		}
		if d.functions == nil {
			d.functions = make(map[string]api.Function)
		}
		d.functions[name] = fn
	}
	res, err := fn.Call(d.ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("aac: %s: %w", name, err)
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}
