// SPDX-License-Identifier: Unlicense OR MIT

// Package lottie renders Lottie and Telegram .tgs animations without cgo.
//
// The renderer is tlottie (https://github.com/dkaraush/tlottie, MIT), a Rust
// library compiled to WebAssembly and executed by wazero, a WebAssembly
// runtime written in Go. Animation JSON arrives from strangers, so it is
// parsed inside the sandbox: the module declares no imports at all, which
// means it cannot reach the filesystem, the network or the host's memory.
// Limits caps what a hostile animation can make a sandbox take: memory, time,
// and the size of the frames copied out of it.
package lottie

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"fmt"
	"image"
	"io"
	"math"
	"time"

	"komarugram/pkg/sandbox"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed tlottie.wasm.gz
var tlottieWasm []byte

// Limits bounds what a hostile animation can make the renderer take.
type Limits struct {
	sandbox.Limits
	// MaxSide is the largest render size accepted. Frames are copied out of
	// the sandbox into Go memory, so this bounds what the host allocates.
	MaxSide int
}

// DefaultLimits suit stickers, which are drawn at up to 512 pixels, doubled
// on a high density screen.
var DefaultLimits = Limits{
	Limits:  sandbox.Limits{Memory: 64 << 20, Slow: 250 * time.Millisecond},
	MaxSide: 1024,
}

// maxFPS is the highest frame rate an animation may declare.
const maxFPS = 240

// Runtime compiles the renderer once and instantiates one sandbox per
// animation. Compiling is the expensive part, so it is worth keeping.
type Runtime struct {
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
	maxSide  int
}

// NewRuntime compiles the embedded WebAssembly module under DefaultLimits.
func NewRuntime(ctx context.Context) (*Runtime, error) {
	return NewRuntimeWithLimits(ctx, DefaultLimits)
}

// NewRuntimeWithLimits compiles the embedded WebAssembly module under limits.
func NewRuntimeWithLimits(ctx context.Context, limits Limits) (*Runtime, error) {
	if limits.MaxSide <= 0 {
		return nil, fmt.Errorf("lottie: render size limit %d is out of range", limits.MaxSide)
	}
	rt, err := sandbox.NewRuntime(ctx, limits.Limits)
	if err != nil {
		return nil, err
	}
	compiled, err := rt.CompileGzipModule(ctx, tlottieWasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile tlottie: %w", err)
	}
	return &Runtime{sandbox: rt, compiled: compiled, maxSide: limits.MaxSide}, nil
}

// Close releases every sandbox created by this runtime.
func (r *Runtime) Close(ctx context.Context) error {
	return r.sandbox.Close(ctx)
}

// Budget returns the memory budget this runtime's sandboxes draw on.
func (r *Runtime) Budget() *sandbox.Budget { return r.sandbox.Budget() }

// Animation is one animation in its own sandbox, rendered at a fixed size.
type Animation struct {
	Name       string
	FrameCount int
	FPS        float64
	Size       image.Point

	// Source is the animation's own canvas size, before scaling.
	Source image.Point

	runtime  *Runtime
	module   api.Module
	instance uint64
	render   api.Function
	frame    *image.RGBA
}

// Open parses data, which may be raw Lottie JSON or a gzipped .tgs, and
// prepares it for rendering at size x size pixels.
func (r *Runtime) Open(ctx context.Context, name string, data []byte, size int) (*Animation, error) {
	if size <= 0 || size > r.maxSide {
		return nil, fmt.Errorf("%s: render size %d is out of range (limit %d)", name, size, r.maxSide)
	}
	animation, err := gunzipIfNeeded(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	module, err := r.sandbox.Instantiate(ctx, r.compiled,
		wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("%s: instantiate: %w", name, err)
	}
	opened, err := r.open(ctx, name, module, animation, size)
	if err != nil {
		_ = module.Close(ctx)
		return nil, err
	}
	return opened, nil
}

// open parses the animation inside module. Parsing is one operation, bound by
// the time limit as a whole.
func (r *Runtime) open(ctx context.Context, name string, module api.Module, animation []byte, size int) (*Animation, error) {
	started := time.Now()

	call := func(fn string, args ...uint64) (uint64, error) {
		f := module.ExportedFunction(fn)
		if f == nil {
			return 0, fmt.Errorf("missing export %s", fn)
		}
		res, err := f.Call(ctx, args...)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", fn, err)
		}
		if len(res) == 0 {
			return 0, nil
		}
		return res[0], nil
	}

	// The sandbox has its own linear memory: the animation has to be copied in.
	ptr, err := call("tlottie_alloc", uint64(len(animation)))
	if err != nil {
		return nil, err
	}
	if !module.Memory().Write(uint32(ptr), animation) {
		return nil, fmt.Errorf("%s: animation does not fit in sandbox memory", name)
	}
	instance, err := call("tlottie_new", ptr, uint64(len(animation)))
	if err != nil {
		return nil, err
	}
	if instance == 0 {
		return nil, fmt.Errorf("%s: rejected by tlottie (malformed or unsupported)", name)
	}

	width, err := call("tlottie_width", instance)
	if err != nil {
		return nil, err
	}
	height, err := call("tlottie_height", instance)
	if err != nil {
		return nil, err
	}
	frames, err := call("tlottie_frame_count", instance)
	if err != nil {
		return nil, err
	}
	rate, err := module.ExportedFunction("tlottie_frame_rate").Call(ctx, instance)
	if err != nil {
		return nil, err
	}
	fps := float64(api.DecodeF32(rate[0]))
	if math.IsNaN(fps) || fps <= 0 || fps > maxFPS {
		return nil, fmt.Errorf("%s: frame rate %v is out of range", name, fps)
	}
	if err := r.sandbox.Check(ctx, module, started); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	return &Animation{
		Name:       name,
		FrameCount: int(uint32(frames)),
		FPS:        fps,
		Size:       image.Pt(size, size),
		Source:     image.Pt(int(uint32(width)), int(uint32(height))),
		runtime:    r,
		module:     module,
		instance:   instance,
		render:     module.ExportedFunction("tlottie_render"),
		frame:      image.NewRGBA(image.Rect(0, 0, size, size)),
	}, nil
}

// FrameAt renders the frame that belongs at elapsed, looping forever, and
// reports how long the renderer took. The returned image is reused between
// calls.
func (a *Animation) FrameAt(ctx context.Context, elapsed time.Duration) (*image.RGBA, time.Duration, error) {
	index := 0
	if a.FrameCount > 0 {
		index = int(elapsed.Seconds()*a.FPS) % a.FrameCount
	}

	started := time.Now()
	res, err := a.render.Call(ctx,
		a.instance,
		uint64(api.EncodeF32(float32(index))),
		uint64(a.Size.X), uint64(a.Size.Y),
		1, // antialias
	)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: render: %w", a.Name, err)
	}
	took := time.Since(started)
	if err := a.runtime.sandbox.Check(ctx, a.module, started); err != nil {
		return nil, took, fmt.Errorf("%s: %w", a.Name, err)
	}
	if res[0] == 0 {
		return nil, took, fmt.Errorf("%s: renderer returned no frame", a.Name)
	}

	pixels, ok := a.module.Memory().Read(uint32(res[0]), uint32(a.Size.X*a.Size.Y*4))
	if !ok {
		return nil, took, fmt.Errorf("%s: frame outside sandbox memory", a.Name)
	}
	// tlottie hands back straight alpha in RGBA byte order, while image.RGBA
	// and Gio both expect the colours premultiplied.
	premultiply(a.frame.Pix, pixels)
	return a.frame, took, nil
}

// Close tears down this animation's sandbox.
func (a *Animation) Close(ctx context.Context) {
	if a.module == nil {
		return
	}
	if drop := a.module.ExportedFunction("tlottie_drop"); drop != nil {
		_, _ = drop.Call(ctx, a.instance)
	}
	_ = a.module.Close(ctx)
	a.module = nil
}

func premultiply(dst, src []byte) {
	for i := 0; i+3 < len(src) && i+3 < len(dst); i += 4 {
		alpha := uint32(src[i+3])
		if alpha == 0xff {
			copy(dst[i:i+4], src[i:i+4])
			continue
		}
		dst[i] = byte(uint32(src[i]) * alpha / 0xff)
		dst[i+1] = byte(uint32(src[i+1]) * alpha / 0xff)
		dst[i+2] = byte(uint32(src[i+2]) * alpha / 0xff)
		dst[i+3] = byte(alpha)
	}
}

// gunzipIfNeeded unpacks .tgs, which is gzipped Lottie JSON.
func gunzipIfNeeded(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	defer reader.Close()
	// A sticker is small; the limit keeps a decompression bomb from taking the
	// client down with it.
	unpacked, err := io.ReadAll(io.LimitReader(reader, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	return unpacked, nil
}
