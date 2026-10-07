// SPDX-License-Identifier: Unlicense OR MIT

// Package h264 decodes the H.264 video Telegram uses for GIFs and animated
// avatars, without cgo and without an FFmpeg of the user's.
//
// The decoder is FFmpeg's, alone, compiled to WebAssembly and executed by
// wazero, as the vp9 package does with libvpx: frames arrive from strangers,
// and a frame that makes the decoder fail takes down nothing but its own
// instance. The container is parsed in Go by the mp4 package, which hands
// over only the compressed samples.
//
// FFmpeg's fast paths are assembly, which WebAssembly cannot take: this
// decoder is several times slower than a native FFmpeg, and is meant for
// machines without one.
//
// The module, avcdec.wasm, is LGPL and is not part of this package: it is
// built and published by https://github.com/komarugif/libavcodec-wasm, and
// the caller hands it to NewRuntime.
package h264

import (
	"context"
	"errors"
	"fmt"
	"image"
	"time"

	"komarugram/pkg/sandbox"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Limits bounds what a hostile file can make the decoder take.
type Limits struct {
	sandbox.Limits
	// MaxSide is the largest picture width or height accepted, whether the
	// container or the bitstream states it.
	MaxSide int
}

// DefaultLimits suit GIFs, which Telegram keeps within HD. A 720x1280
// picture keeps about 13 MiB in its decoder and takes about 10 ms.
var DefaultLimits = Limits{
	Limits:  sandbox.Limits{Memory: 96 << 20, Slow: time.Second},
	MaxSide: 1920,
}

// Statuses of the shim besides 0 and errors, as h264shim.c defines them.
const (
	statusAgain = 1
	statusEOF   = 2
)

// errAgain and errEOF are the decoder asking for samples and saying it has
// put out every picture of the stream.
var (
	errAgain = errors.New("h264: needs samples")
	errEOF   = errors.New("h264: end of stream")
)

// Runtime compiles the decoder once; each stream then gets its own instance.
type Runtime struct {
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
	maxSide  int
}

// NewRuntime compiles module, an avcdec.wasm, under DefaultLimits.
func NewRuntime(ctx context.Context, module []byte) (*Runtime, error) {
	return NewRuntimeWithLimits(ctx, module, DefaultLimits)
}

// NewRuntimeWithLimits compiles module, an avcdec.wasm, under limits.
func NewRuntimeWithLimits(ctx context.Context, module []byte, limits Limits) (*Runtime, error) {
	if limits.MaxSide <= 0 {
		return nil, fmt.Errorf("h264: picture side limit %d is out of range", limits.MaxSide)
	}
	rt, err := sandbox.NewRuntime(ctx, limits.Limits)
	if err != nil {
		return nil, err
	}
	// FFmpeg's libc wants the WASI imports. It gets no preopened
	// directories, no arguments and no environment.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt.Wazero()); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wasi: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, module)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile h264: %w", err)
	}
	return &Runtime{sandbox: rt, compiled: compiled, maxSide: limits.MaxSide}, nil
}

// Close tears down every decoder created by this runtime.
func (r *Runtime) Close(ctx context.Context) error {
	return r.sandbox.Close(ctx)
}

// Budget returns the memory budget this runtime's decoders draw on.
func (r *Runtime) Budget() *sandbox.Budget { return r.sandbox.Budget() }

// Decoder is one H.264 stream in its own sandbox.
type Decoder struct {
	runtime   *Runtime
	module    api.Module
	handle    uint64
	functions map[string]api.Function
	// frame is the last picture, read in place in sandbox memory.
	frame image.YCbCr
}

// NewDecoder starts a decoder for samples described by config, an avcC
// record.
func (r *Runtime) NewDecoder(ctx context.Context, config []byte) (*Decoder, error) {
	module, err := r.sandbox.Instantiate(ctx, r.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate h264: %w", err)
	}
	started := time.Now()
	d := &Decoder{runtime: r, module: module}
	ptr, err := d.write(ctx, config)
	if err == nil {
		d.handle, err = d.call(ctx, "h264w_new", ptr, uint64(len(config)))
		d.call(ctx, "h264w_dealloc", ptr) //nolint:errcheck // freeing cannot fail usefully
	}
	if err == nil && d.handle == 0 {
		err = fmt.Errorf("h264: bad decoder configuration")
	}
	if err == nil {
		err = r.sandbox.Check(ctx, module, started)
	}
	if err != nil {
		_ = module.Close(ctx)
		return nil, err
	}
	return d, nil
}

func (d *Decoder) call(ctx context.Context, name string, args ...uint64) (uint64, error) {
	fn := d.functions[name]
	if fn == nil {
		fn = d.module.ExportedFunction(name)
		if fn == nil {
			return 0, fmt.Errorf("h264: missing export %s", name)
		}
		if d.functions == nil {
			d.functions = make(map[string]api.Function)
		}
		d.functions[name] = fn
	}
	res, err := fn.Call(ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("h264: %s: %w", name, err)
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

// write copies data into a buffer of the sandbox, which the caller frees.
func (d *Decoder) write(ctx context.Context, data []byte) (uint64, error) {
	ptr, err := d.call(ctx, "h264w_alloc", uint64(len(data)))
	if err != nil {
		return 0, err
	}
	if ptr == 0 {
		return 0, fmt.Errorf("h264: sandbox out of memory")
	}
	if !d.module.Memory().Write(uint32(ptr), data) {
		return 0, fmt.Errorf("h264: %d bytes do not fit in sandbox memory", len(data))
	}
	return ptr, nil
}

// SetFast skips the deblocking filter: faster, with blockier pictures.
func (d *Decoder) SetFast(ctx context.Context, fast bool) error {
	var on uint64
	if fast {
		on = 1
	}
	_, err := d.call(ctx, "h264w_fast", d.handle, on)
	return err
}

// Send feeds one sample, or nil for the end of the stream. It fails with
// errAgain when pictures must be received first.
func (d *Decoder) Send(ctx context.Context, sample []byte) error {
	started := time.Now()
	var status uint64
	if len(sample) == 0 {
		var err error
		if status, err = d.call(ctx, "h264w_send", d.handle, 0, 0); err != nil {
			return err
		}
	} else {
		ptr, err := d.write(ctx, sample)
		if err != nil {
			return err
		}
		status, err = d.call(ctx, "h264w_send", d.handle, ptr, uint64(len(sample)))
		d.call(ctx, "h264w_dealloc", ptr) //nolint:errcheck // freeing cannot fail usefully
		if err != nil {
			return err
		}
	}
	if err := d.runtime.sandbox.Check(ctx, d.module, started); err != nil {
		return fmt.Errorf("h264: %w", err)
	}
	return statusError(int32(status))
}

// StreamError is FFmpeg failing on the stream: a broken sample, which it
// conceals when it can, rather than the sandbox failing.
type StreamError struct{ Status int32 }

func (e *StreamError) Error() string {
	return fmt.Sprintf("h264: decoding failed with status %d", e.Status)
}

func statusError(status int32) error {
	switch status {
	case 0:
		return nil
	case statusAgain:
		return errAgain
	case statusEOF:
		return errEOF
	}
	return &StreamError{status}
}

// Receive returns the next picture as planar YCbCr, or errAgain when the
// decoder needs samples, or errEOF after the end of the stream. The image
// lies in the sandbox's memory: it holds until the next call into the
// decoder, or Close.
func (d *Decoder) Receive(ctx context.Context) (*image.YCbCr, error) {
	started := time.Now()
	status, err := d.call(ctx, "h264w_receive", d.handle)
	if err != nil {
		return nil, err
	}
	if err := d.runtime.sandbox.Check(ctx, d.module, started); err != nil {
		return nil, fmt.Errorf("h264: %w", err)
	}
	if err := statusError(int32(status)); err != nil {
		return nil, err
	}
	return d.readFrame(ctx)
}

// Flush forgets every sample sent, to start again from a keyframe.
func (d *Decoder) Flush(ctx context.Context) error {
	_, err := d.call(ctx, "h264w_flush", d.handle)
	return err
}

// readFrame returns the picture's planes, in place in the sandbox's memory.
func (d *Decoder) readFrame(ctx context.Context) (*image.YCbCr, error) {
	var got [3]uint64
	for i, name := range []string{"h264w_width", "h264w_height", "h264w_yuv420"} {
		v, err := d.call(ctx, name, d.handle)
		if err != nil {
			return nil, err
		}
		got[i] = v
	}
	w, h := int(int32(got[0])), int(int32(got[1]))
	if got[2] != 1 {
		return nil, fmt.Errorf("h264: pictures are not 8-bit 4:2:0")
	}
	if w <= 0 || h <= 0 || w > d.runtime.maxSide || h > d.runtime.maxSide {
		return nil, fmt.Errorf("h264: picture size %dx%d is over the limit of %d", w, h, d.runtime.maxSide)
	}
	heights := [3]int{h, (h + 1) / 2, (h + 1) / 2}
	widths := [3]int{w, (w + 1) / 2, (w + 1) / 2}
	var views [3][]byte
	var strides [3]int
	for plane := range views {
		ptr, err := d.call(ctx, "h264w_plane", d.handle, uint64(plane))
		if err != nil {
			return nil, err
		}
		stride, err := d.call(ctx, "h264w_stride", d.handle, uint64(plane))
		if err != nil {
			return nil, err
		}
		strides[plane] = int(int32(stride))
		if ptr == 0 || strides[plane] < widths[plane] {
			return nil, fmt.Errorf("h264: plane %d is missing", plane)
		}
		// The last row need not be padded to the stride.
		size := strides[plane]*(heights[plane]-1) + widths[plane]
		view, ok := d.module.Memory().Read(uint32(ptr), uint32(size))
		if !ok {
			return nil, fmt.Errorf("h264: plane %d lies outside sandbox memory", plane)
		}
		views[plane] = view
	}
	if strides[1] != strides[2] {
		return nil, fmt.Errorf("h264: chroma planes of strides %d and %d", strides[1], strides[2])
	}
	d.frame = image.YCbCr{Y: views[0], Cb: views[1], Cr: views[2], YStride: strides[0], CStride: strides[1],
		SubsampleRatio: image.YCbCrSubsampleRatio420, Rect: image.Rect(0, 0, w, h)}
	return &d.frame, nil
}

// Close releases this decoder's sandbox.
func (d *Decoder) Close(ctx context.Context) {
	if d.module == nil {
		return
	}
	if d.handle != 0 {
		_, _ = d.call(ctx, "h264w_drop", d.handle)
	}
	_ = d.module.Close(ctx)
	d.module = nil
	// A picture read in place would keep the sandbox's memory alive.
	d.frame = image.YCbCr{}
}
