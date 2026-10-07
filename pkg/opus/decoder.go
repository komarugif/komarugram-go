// SPDX-License-Identifier: Unlicense OR MIT

// Package opus decodes the Opus audio of Telegram's voice messages, without
// cgo and without an FFmpeg.
//
// The decoder is libopus's, compiled to WebAssembly and executed by wazero,
// as the vp9 package does with libvpx: packets arrive from strangers, and one
// that makes the decoder fail takes down nothing but its own instance.
// libopus is BSD-licensed, so the module is embedded; its notice, which a
// binary distribution carries, is COPYING.libopus. The OGG container is
// parsed in Go, and only packets reach the sandbox.
package opus

import (
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"time"

	"komarugram/pkg/sandbox"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed opusdec.wasm
var opusWasm []byte

// Rate is the sample rate the decoder puts out, the rate of every Opus
// stream.
const Rate = 48000

// maxPacket is the largest packet accepted: RFC 6716 allows 48 frames of
// 1275 bytes. maxSamples is the most a packet decodes to, 120 ms.
const (
	maxPacket  = 48 * 1275
	maxSamples = Rate * 120 / 1000
)

// DefaultLimits suit a voice message: a decoder keeps about 30 KiB, and a
// packet takes well under a millisecond.
var DefaultLimits = sandbox.Limits{Memory: 4 << 20, Slow: 250 * time.Millisecond}

// Runtime compiles the decoder once; each stream then gets its own instance.
type Runtime struct {
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
}

// NewRuntime compiles the embedded libopus build under DefaultLimits.
func NewRuntime(ctx context.Context) (*Runtime, error) {
	rt, err := sandbox.NewRuntime(ctx, DefaultLimits)
	if err != nil {
		return nil, err
	}
	// libopus's libc wants the WASI imports. It gets no preopened
	// directories, no arguments and no environment.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt.Wazero()); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wasi: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, opusWasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile libopus: %w", err)
	}
	return &Runtime{sandbox: rt, compiled: compiled}, nil
}

// Close tears down every decoder created by this runtime.
func (r *Runtime) Close(ctx context.Context) error { return r.sandbox.Close(ctx) }

// Decoder is one Opus stream in its own sandbox, decoded to mono.
type Decoder struct {
	runtime   *Runtime
	module    api.Module
	functions map[string]api.Function
	handle    uint64
	// in and out are buffers in sandbox memory for a packet and its
	// samples.
	in, out uint64
	pcm     []int16
}

// NewDecoder starts a decoder instance.
func (r *Runtime) NewDecoder(ctx context.Context) (*Decoder, error) {
	module, err := r.sandbox.Instantiate(ctx, r.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate libopus: %w", err)
	}
	d := &Decoder{runtime: r, module: module, pcm: make([]int16, maxSamples)}
	d.handle, err = d.call(ctx, "opusw_new")
	if err == nil && d.handle == 0 {
		err = fmt.Errorf("opus: decoder init failed")
	}
	if err == nil {
		d.in, err = d.call(ctx, "opusw_alloc", maxPacket)
	}
	if err == nil {
		d.out, err = d.call(ctx, "opusw_alloc", 2*maxSamples)
	}
	if err == nil && (d.in == 0 || d.out == 0) {
		err = fmt.Errorf("opus: sandbox out of memory")
	}
	if err != nil {
		_ = module.Close(ctx)
		return nil, err
	}
	return d, nil
}

// Close ends the decoder and its sandbox.
func (d *Decoder) Close(ctx context.Context) error { return d.module.Close(ctx) }

// Reset forgets what the decoder heard, before it decodes from another
// point of the stream.
func (d *Decoder) Reset(ctx context.Context) error {
	_, err := d.call(ctx, "opusw_reset", d.handle)
	return err
}

// Decode decodes one packet and returns its samples, 48 kHz mono. They are
// valid until the next call. A decode that overruns the time limit closes
// the decoder.
func (d *Decoder) Decode(ctx context.Context, packet []byte) ([]int16, error) {
	if len(packet) == 0 || len(packet) > maxPacket {
		return nil, fmt.Errorf("opus: packet of %d bytes", len(packet))
	}
	started := time.Now()
	if !d.module.Memory().Write(uint32(d.in), packet) {
		return nil, fmt.Errorf("opus: packet does not fit in sandbox memory")
	}
	n, err := d.call(ctx, "opusw_decode", d.handle, d.in, uint64(len(packet)), d.out, maxSamples)
	if err != nil {
		return nil, err
	}
	if err := d.runtime.sandbox.Check(ctx, d.module, started); err != nil {
		return nil, fmt.Errorf("opus: %w", err)
	}
	count := int32(n)
	if count < 0 || count > maxSamples {
		return nil, fmt.Errorf("opus: decode failed with status %d", count)
	}
	raw, ok := d.module.Memory().Read(uint32(d.out), uint32(2*count))
	if !ok {
		return nil, fmt.Errorf("opus: samples lie outside sandbox memory")
	}
	for i := range int(count) {
		d.pcm[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return d.pcm[:count], nil
}

func (d *Decoder) call(ctx context.Context, name string, args ...uint64) (uint64, error) {
	fn := d.functions[name]
	if fn == nil {
		fn = d.module.ExportedFunction(name)
		if fn == nil {
			return 0, fmt.Errorf("opus: missing export %s", name)
		}
		if d.functions == nil {
			d.functions = make(map[string]api.Function)
		}
		d.functions[name] = fn
	}
	res, err := fn.Call(ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("opus: %s: %w", name, err)
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}
