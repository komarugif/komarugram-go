// SPDX-License-Identifier: Unlicense OR MIT

// Package drdec decodes MP3, FLAC and WAV, without cgo: the voice messages
// some bots and clients send in MP3, and audio files.
//
// The decoders are dr_libs's (https://github.com/mackron/dr_libs), compiled
// to WebAssembly and executed by wazero, as the vp9 and opus packages do:
// files arrive from strangers, and one that makes a decoder fail takes down
// nothing but its own instance. dr_libs is in the public domain, or MIT No
// Attribution, so the module is embedded.
package drdec

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"time"

	"komarugram/pkg/audio"
	"komarugram/pkg/sandbox"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed drdec.wasm
var drWasm []byte

// Format is a kind of file the module decodes, as drshim.c numbers them.
type Format int

const (
	MP3  Format = 1
	FLAC Format = 2
	WAV  Format = 3
)

// FormatOf is the format of a file of MIME type mime, 0 for one the module
// does not decode.
func FormatOf(mime string) Format {
	switch mime {
	case "audio/mpeg", "audio/mp3":
		return MP3
	case "audio/flac", "audio/x-flac":
		return FLAC
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return WAV
	}
	return 0
}

// chunk is the most frames a read puts out, DRW_CHUNK in drshim.c.
const chunk = 4096

// DefaultLimits leave room for the decoder: the file is read through the
// host, a range at a time, and is never all in the sandbox. Opening an MP3
// exactly reads it through to count its frames, which takes a long file a
// while; the time the host takes to read is not counted.
var DefaultLimits = sandbox.Limits{Memory: 32 << 20, Slow: 3 * time.Second}

// Runtime compiles the module once; each file then gets its own instance.
type Runtime struct {
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
}

// NewRuntime compiles the embedded module under DefaultLimits.
func NewRuntime(ctx context.Context) (*Runtime, error) {
	rt, err := sandbox.NewRuntime(ctx, DefaultLimits)
	if err != nil {
		return nil, err
	}
	// dr_libs's libc wants the WASI imports. It gets no preopened
	// directories, no arguments and no environment.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt.Wazero()); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wasi: %w", err)
	}
	// The file is read through host.read, from the file of the decoder
	// that calls, which its context carries.
	if _, err := rt.Wazero().NewHostModuleBuilder("host").NewFunctionBuilder().WithFunc(hostRead).Export("read").Instantiate(ctx); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("host module: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, drWasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile dr_libs: %w", err)
	}
	return &Runtime{sandbox: rt, compiled: compiled}, nil
}

// Close tears down every decoder created by this runtime.
func (r *Runtime) Close(ctx context.Context) error { return r.sandbox.Close(ctx) }

// fileKey carries the file a decoder reads in the context of its calls.
type fileKey struct{}

// fileState is what a decoder reads, and how long reading took.
type fileState struct {
	r     io.ReaderAt
	size  int64
	spent time.Duration
}

// hostRead is host.read: n bytes of the calling decoder's file at offset
// into ptr in its memory; the bytes read, or -1.
func hostRead(ctx context.Context, m api.Module, offset int64, ptr, n int32) int32 {
	f, _ := ctx.Value(fileKey{}).(*fileState)
	if f == nil || offset < 0 || n < 0 {
		return -1
	}
	if offset >= f.size || n == 0 {
		return 0
	}
	n = int32(min(int64(n), f.size-offset))
	dst, ok := m.Memory().Read(uint32(ptr), uint32(n))
	if !ok {
		return -1
	}
	started := time.Now()
	k, err := f.r.ReadAt(dst, offset)
	f.spent += time.Since(started)
	if k == 0 && err != nil {
		return -1
	}
	return int32(k)
}

// Decoder is one file in its own sandbox, read as stereo frames at the
// file's own rate: see audio.Resample.
type Decoder struct {
	ctx       context.Context
	file      *fileState
	runtime   *Runtime
	module    api.Module
	functions map[string]api.Function
	handle    uint64
	// format is the file's; table is set when an MP3 was read through to make
	// a table of its frames, which a move then goes by; moves counts the moves
	// asked of the module.
	format   Format
	table    bool
	moves    int
	channels int
	rate     int64
	frames   int64
	// pos is the next frame Read puts out; ready are frames decoded and not
	// put out yet.
	pos   int64
	ready []audio.Frame
	pcm   []audio.Frame
}

// Open decodes data, a file of format, all there. Reads go on under ctx.
func (r *Runtime) Open(ctx context.Context, format Format, data []byte) (*Decoder, error) {
	return r.OpenAt(ctx, format, bytes.NewReader(data), int64(len(data)), 0)
}

// OpenAt decodes the file of format, of size bytes, that file reads, a range
// at a time, as it plays: a file that downloads as it plays need not be all
// there. An MP3 that is not knows its length from length, which Telegram
// tells; with length 0, it is read through to count its frames. Reads go on
// under ctx.
func (r *Runtime) OpenAt(ctx context.Context, format Format, file io.ReaderAt, size int64, length time.Duration) (*Decoder, error) {
	if size <= 0 {
		return nil, fmt.Errorf("drdec: file of %d bytes", size)
	}
	f := &fileState{r: file, size: size}
	ctx = context.WithValue(ctx, fileKey{}, f)
	module, err := r.sandbox.Instantiate(ctx, r.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate dr_libs: %w", err)
	}
	d := &Decoder{ctx: ctx, file: f, runtime: r, module: module, pcm: make([]audio.Frame, chunk)}
	if err := d.open(format, size, length); err != nil {
		_ = module.Close(ctx)
		return nil, err
	}
	return d, nil
}

func (d *Decoder) open(format Format, size int64, length time.Duration) error {
	exact := uint64(0)
	if length <= 0 {
		exact = 1
	}
	var err error
	if d.handle, err = d.call("drw_open", uint64(format), uint64(size), exact); err != nil {
		return err
	}
	if d.handle == 0 {
		return errors.New("drdec: not a file of its format")
	}
	d.format, d.table = format, exact != 0
	channels, err := d.call("drw_channels", d.handle)
	if err != nil {
		return err
	}
	rate, err := d.call("drw_rate", d.handle)
	if err != nil {
		return err
	}
	frames, err := d.call("drw_frames", d.handle)
	if err != nil {
		return err
	}
	d.channels, d.rate, d.frames = int(int32(channels)), int64(int32(rate)), int64(frames)
	if d.frames <= 0 && length > 0 {
		d.frames = int64(length) * d.rate / int64(time.Second)
	}
	if d.channels < 1 || d.channels > 8 || d.rate <= 0 || d.frames <= 0 {
		return errors.New("drdec: no sound")
	}
	return nil
}

// Close ends the decoder and its sandbox.
func (d *Decoder) Close() error { return d.module.Close(context.Background()) }

// Rate is the file's sample rate.
func (d *Decoder) Rate() int64 { return d.rate }

// Frames is how many samples, a channel, the file plays.
func (d *Decoder) Frames() int64 { return d.frames }

// Position is the next sample Read puts out.
func (d *Decoder) Position() int64 { return d.pos }

// seekSpan is how much sound an MP3 without a table of its frames is moved
// over in one call of the module: dr_libs goes forward by decoding, from the
// start when it is moving back, at something like five hundred times the
// speed of the sound, and a call that takes over DefaultLimits.Slow ends the
// decoder. A move over an hour is made of moves over less than this.
var seekSpan = 200 * time.Second

// SeekSample moves to sample pos of the file.
func (d *Decoder) SeekSample(pos int64) error {
	pos = max(0, min(pos, d.frames))
	if d.format == MP3 && !d.table {
		from := d.pos
		if pos < from {
			if err := d.move(0); err != nil {
				return err
			}
			from = 0
		}
		for span := max(1, int64(seekSpan)*d.rate/int64(time.Second)); pos-from > span; from += span {
			if err := d.move(from + span); err != nil {
				return err
			}
		}
	}
	return d.move(pos)
}

// move asks the module to move to sample pos.
func (d *Decoder) move(pos int64) error {
	d.moves++
	ok, err := d.call("drw_seek", d.handle, uint64(pos))
	if err != nil {
		return err
	}
	if int32(ok) == 0 {
		return errors.New("drdec: seek failed")
	}
	d.pos, d.ready = pos, nil
	return nil
}

// Read fills out with the next frames, folded to stereo, and returns
// io.EOF at the end of the file.
func (d *Decoder) Read(out []audio.Frame) (int, error) {
	if len(d.ready) == 0 {
		if err := d.decode(); err != nil {
			return 0, err
		}
	}
	n := copy(out, d.ready)
	d.ready = d.ready[n:]
	d.pos += int64(n)
	return n, nil
}

// decode reads the next frames into ready.
func (d *Decoder) decode() error {
	n, err := d.call("drw_read", d.handle)
	if err != nil {
		return err
	}
	frames := int(int32(n))
	if frames <= 0 {
		return io.EOF
	}
	if frames > chunk {
		return errors.New("drdec: read too much")
	}
	ptr, err := d.call("drw_pcm", d.handle)
	if err != nil {
		return err
	}
	raw, ok := d.module.Memory().Read(uint32(ptr), uint32(2*frames*d.channels))
	if !ok {
		return errors.New("drdec: samples lie outside sandbox memory")
	}
	d.ready = audio.Downmix(d.pcm, raw, d.channels)
	return nil
}

// call calls export name, and closes the sandbox when it took longer than
// the limit, the time the host took to read the file left out.
func (d *Decoder) call(name string, args ...uint64) (uint64, error) {
	started, spent := time.Now(), d.file.spent
	res, err := d.callRaw(name, args...)
	if err != nil {
		return 0, err
	}
	if err := d.runtime.sandbox.Check(d.ctx, d.module, started.Add(d.file.spent-spent)); err != nil {
		return 0, fmt.Errorf("drdec: %w", err)
	}
	return res, nil
}

func (d *Decoder) callRaw(name string, args ...uint64) (uint64, error) {
	fn := d.functions[name]
	if fn == nil {
		fn = d.module.ExportedFunction(name)
		if fn == nil {
			return 0, fmt.Errorf("drdec: missing export %s", name)
		}
		if d.functions == nil {
			d.functions = make(map[string]api.Function)
		}
		d.functions[name] = fn
	}
	res, err := fn.Call(d.ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("drdec: %s: %w", name, err)
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}
