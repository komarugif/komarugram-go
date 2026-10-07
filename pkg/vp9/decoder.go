// SPDX-License-Identifier: Unlicense OR MIT

// Package vp9 decodes the VP9 video Telegram uses for animated stickers and
// custom emoji, without cgo.
//
// The decoder is libvpx compiled to WebAssembly and executed by wazero. Frames
// arrive from strangers, so the decoder runs in a sandbox: it can only read the
// bytes the host writes into its linear memory, and a frame that makes it fail
// takes down nothing but its own instance. Limits caps what a hostile file can
// make an instance take: memory, time, and the size of the frames copied out
// of it. The container is not parsed here at all — see the webm package, which
// does that in Go and hands over only the compressed frames.
package vp9

import (
	"context"
	_ "embed"
	"fmt"
	"image"
	"time"

	"komarugram/pkg/sandbox"
	"komarugram/pkg/webm"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed vpxdec.wasm.gz
var vpxWasm []byte

// Limits bounds what a hostile file can make the decoder take.
type Limits struct {
	sandbox.Limits
	// MaxSide is the largest frame width or height accepted, whether the
	// container or the bitstream states it. Frames are copied out of the
	// sandbox into Go memory, so this bounds what the host allocates.
	MaxSide int
}

// DefaultLimits suit stickers and custom emoji, which Telegram caps at 512
// pixels a side. A 512x512 sticker keeps about 3 MiB in its decoder and takes
// a few milliseconds a frame.
var DefaultLimits = Limits{
	Limits:  sandbox.Limits{Memory: 32 << 20, Slow: 250 * time.Millisecond},
	MaxSide: 512,
}

// Runtime compiles the decoder once; each stream then gets its own instance.
type Runtime struct {
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
	maxSide  int
}

// NewRuntime compiles the embedded libvpx build under DefaultLimits.
func NewRuntime(ctx context.Context) (*Runtime, error) {
	return NewRuntimeWithLimits(ctx, DefaultLimits)
}

// NewRuntimeWithLimits compiles the embedded libvpx build under limits.
func NewRuntimeWithLimits(ctx context.Context, limits Limits) (*Runtime, error) {
	if limits.MaxSide <= 0 {
		return nil, fmt.Errorf("vp9: frame side limit %d is out of range", limits.MaxSide)
	}
	rt, err := sandbox.NewRuntime(ctx, limits.Limits)
	if err != nil {
		return nil, err
	}
	// libvpx is a C library, so its libc wants the WASI imports. It gets no
	// preopened directories, no arguments and no environment.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt.Wazero()); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wasi: %w", err)
	}
	compiled, err := rt.CompileGzipModule(ctx, vpxWasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile libvpx: %w", err)
	}
	return &Runtime{sandbox: rt, compiled: compiled, maxSide: limits.MaxSide}, nil
}

// Close tears down every decoder created by this runtime.
func (r *Runtime) Close(ctx context.Context) error {
	return r.sandbox.Close(ctx)
}

// Budget returns the memory budget this runtime's decoders draw on.
func (r *Runtime) Budget() *sandbox.Budget { return r.sandbox.Budget() }

// Decoder is one VP9 stream in its own sandbox.
type Decoder struct {
	runtime *Runtime
	module  api.Module
	handle  uint64
	scratch *image.YCbCr
	// frame is the last frame, read in place in sandbox memory.
	frame     image.YCbCr
	functions map[string]api.Function
}

// NewDecoder starts a decoder instance.
func (r *Runtime) NewDecoder(ctx context.Context) (*Decoder, error) {
	module, err := r.sandbox.Instantiate(ctx, r.compiled,
		wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate libvpx: %w", err)
	}
	started := time.Now()
	decoder := &Decoder{runtime: r, module: module}
	handle, err := decoder.call(ctx, "vpxw_new")
	if err == nil && handle == 0 {
		err = fmt.Errorf("vp9: decoder init failed")
	}
	if err == nil {
		err = r.sandbox.Check(ctx, module, started)
	}
	if err != nil {
		_ = module.Close(ctx)
		return nil, err
	}
	decoder.handle = handle
	return decoder, nil
}

func (d *Decoder) call(ctx context.Context, name string, args ...uint64) (uint64, error) {
	fn := d.functions[name]
	if fn == nil {
		fn = d.module.ExportedFunction(name)
		if fn == nil {
			return 0, fmt.Errorf("vp9: missing export %s", name)
		}
		if d.functions == nil {
			d.functions = make(map[string]api.Function)
		}
		d.functions[name] = fn
	}
	res, err := fn.Call(ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("vp9: %s: %w", name, err)
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

// Decode decodes one compressed frame and returns it as planar YCbCr. The
// image lies in the sandbox's memory: it holds until the next call into the
// decoder, or Close. A decode that overruns the time limit closes the
// decoder.
func (d *Decoder) Decode(ctx context.Context, packet []byte) (*image.YCbCr, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("vp9: empty packet")
	}
	started := time.Now()
	frame, err := d.decode(ctx, packet)
	if err != nil {
		return nil, err
	}
	if err := d.runtime.sandbox.Check(ctx, d.module, started); err != nil {
		return nil, fmt.Errorf("vp9: %w", err)
	}
	return frame, nil
}

func (d *Decoder) decode(ctx context.Context, packet []byte) (*image.YCbCr, error) {
	ptr, err := d.call(ctx, "vpxw_alloc", uint64(len(packet)))
	if err != nil {
		return nil, err
	}
	if ptr == 0 {
		return nil, fmt.Errorf("vp9: sandbox out of memory")
	}
	defer d.call(ctx, "vpxw_dealloc", ptr) //nolint:errcheck // freeing cannot fail usefully

	if !d.module.Memory().Write(uint32(ptr), packet) {
		return nil, fmt.Errorf("vp9: packet does not fit in sandbox memory")
	}
	status, err := d.call(ctx, "vpxw_decode", d.handle, ptr, uint64(len(packet)))
	if err != nil {
		// A trap means the decoder bailed out inside the sandbox; the instance
		// cannot be reused, but nothing outside it is affected.
		return nil, err
	}
	if int32(status) != 0 {
		return nil, fmt.Errorf("vp9: decode failed with status %d", int32(status))
	}
	return d.readFrame(ctx)
}

// readFrame returns the decoded planes, in place in the sandbox's memory.
func (d *Decoder) readFrame(ctx context.Context) (*image.YCbCr, error) {
	width, err := d.call(ctx, "vpxw_width", d.handle)
	if err != nil {
		return nil, err
	}
	height, err := d.call(ctx, "vpxw_height", d.handle)
	if err != nil {
		return nil, err
	}
	subsampling, err := d.call(ctx, "vpxw_subsampling", d.handle)
	if err != nil {
		return nil, err
	}
	if int32(subsampling) != 11 {
		return nil, fmt.Errorf("vp9: unsupported chroma subsampling %d", int32(subsampling))
	}
	w, h := int(uint32(width)), int(uint32(height))
	if w <= 0 || h <= 0 || w > d.runtime.maxSide || h > d.runtime.maxSide {
		return nil, fmt.Errorf("vp9: frame size %dx%d is over the limit of %d", w, h, d.runtime.maxSide)
	}

	heights := [3]int{h, (h + 1) / 2, (h + 1) / 2}
	widths := [3]int{w, (w + 1) / 2, (w + 1) / 2}
	var ptrs, strides [3]int
	for plane := range ptrs {
		ptr, err := d.call(ctx, "vpxw_plane", d.handle, uint64(plane))
		if err != nil {
			return nil, err
		}
		stride, err := d.call(ctx, "vpxw_stride", d.handle, uint64(plane))
		if err != nil {
			return nil, err
		}
		if ptr == 0 || int32(stride) < int32(widths[plane]) {
			return nil, fmt.Errorf("vp9: plane %d is missing", plane)
		}
		ptrs[plane], strides[plane] = int(uint32(ptr)), int(int32(stride))
	}
	var views [3][]byte
	for plane := range views {
		// The last row need not be padded to the stride.
		size := strides[plane]*(heights[plane]-1) + widths[plane]
		view, ok := d.module.Memory().Read(uint32(ptrs[plane]), uint32(size))
		if !ok {
			return nil, fmt.Errorf("vp9: plane %d lies outside sandbox memory", plane)
		}
		views[plane] = view
	}
	if strides[1] == strides[2] {
		// The frame is read where libvpx left it, in sandbox memory, with
		// its padded rows: nothing is copied. The next call into the
		// module may change it, as Decode says.
		d.frame = image.YCbCr{Y: views[0], Cb: views[1], Cr: views[2], YStride: strides[0], CStride: strides[1],
			SubsampleRatio: image.YCbCrSubsampleRatio420, Rect: image.Rect(0, 0, w, h)}
		return &d.frame, nil
	}
	// image.YCbCr has one chroma stride; copy into a packed image.
	if d.scratch == nil || d.scratch.Rect.Dx() != w || d.scratch.Rect.Dy() != h {
		d.scratch = image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	}
	planes := [3][]byte{d.scratch.Y, d.scratch.Cb, d.scratch.Cr}
	packed := [3]int{d.scratch.YStride, d.scratch.CStride, d.scratch.CStride}
	for plane := range planes {
		for row := range heights[plane] {
			copy(planes[plane][row*packed[plane]:row*packed[plane]+widths[plane]], views[plane][row*strides[plane]:])
		}
	}
	return d.scratch, nil
}

// Close releases this decoder's sandbox.
func (d *Decoder) Close(ctx context.Context) {
	if d.module == nil {
		return
	}
	if d.handle != 0 {
		_, _ = d.call(ctx, "vpxw_drop", d.handle)
	}
	_ = d.module.Close(ctx)
	d.module = nil
	// A frame read in place would keep the sandbox's memory alive.
	d.frame, d.scratch = image.YCbCr{}, nil
}

// CodecID is the Matroska codec id this package decodes.
const CodecID = "V_VP9"

// Supported reports whether a demuxed file can be played here.
func Supported(file *webm.File) bool {
	return file != nil && file.CodecID == CodecID
}
