// SPDX-License-Identifier: Unlicense OR MIT

package vp9

import (
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"time"

	"komarugram/pkg/sandbox"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// The encoder is libvpx's VP9 encoder compiled to WebAssembly
// (build/vpxencshim.c), run by wazero in a sandbox as the decoder is. What
// it encodes is the client's own pictures, as the frames of a Lottie
// sticker drawn: the sandbox keeps libvpx's memory and time bounded all
// the same.

//go:embed vpxenc.wasm.zst
var vpxencWasm []byte

// EncoderLimits suit stickers, 512 pixels a side: an encoder of such pictures
// keeps tens of MiB and takes tens of milliseconds a frame.
var EncoderLimits = sandbox.Limits{Memory: 192 << 20, Slow: 5 * time.Second}

// EncoderRuntime compiles the encoder once; each stream then gets its own
// instance.
type EncoderRuntime struct {
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
}

// NewEncoderRuntime compiles the embedded encoder under limits.
func NewEncoderRuntime(ctx context.Context, limits sandbox.Limits) (*EncoderRuntime, error) {
	rt, err := sandbox.NewRuntime(ctx, limits)
	if err != nil {
		return nil, err
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt.Wazero()); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wasi: %w", err)
	}
	compiled, err := rt.CompileZstdModule(ctx, vpxencWasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile libvpx's encoder: %w", err)
	}
	return &EncoderRuntime{sandbox: rt, compiled: compiled}, nil
}

// Close tears down every encoder of the runtime.
func (r *EncoderRuntime) Close(ctx context.Context) error { return r.sandbox.Close(ctx) }

// EncoderConfig is what an encoder makes: pictures of Width x Height, FPS a
// second, at quality CQ (0 best, 63 worst) and Speed (0 slowest, 9 fastest).
type EncoderConfig struct {
	Width, Height, FPS int
	CQ, Speed          int
}

// Packet is a compressed frame.
type Packet struct {
	Data []byte
	Key  bool
	PTS  int64
}

// Encoder is one VP9 stream in its own sandbox, of I420 pictures.
type Encoder struct {
	runtime   *EncoderRuntime
	module    api.Module
	handle    uint64
	config    EncoderConfig
	functions map[string]api.Function
}

// NewEncoder starts an encoder.
func (r *EncoderRuntime) NewEncoder(ctx context.Context, config EncoderConfig) (*Encoder, error) {
	if config.Width <= 0 || config.Height <= 0 || config.Width%2 != 0 || config.Height%2 != 0 {
		return nil, fmt.Errorf("vp9: pictures of %dx%d", config.Width, config.Height)
	}
	module, err := r.sandbox.Instantiate(ctx, r.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate libvpx's encoder: %w", err)
	}
	e := &Encoder{runtime: r, module: module, config: config}
	started := time.Now()
	handle, err := e.call(ctx, "vpxe_new", uint64(config.Width), uint64(config.Height), uint64(config.FPS), uint64(config.CQ), uint64(config.Speed))
	if err == nil && handle == 0 {
		err = fmt.Errorf("vp9: encoder init failed")
	}
	if err == nil {
		err = r.sandbox.Check(ctx, module, started)
	}
	if err != nil {
		_ = module.Close(ctx)
		return nil, err
	}
	e.handle = handle
	return e, nil
}

func (e *Encoder) call(ctx context.Context, name string, args ...uint64) (uint64, error) {
	fn := e.functions[name]
	if fn == nil {
		fn = e.module.ExportedFunction(name)
		if fn == nil {
			return 0, fmt.Errorf("vp9: missing export %s", name)
		}
		if e.functions == nil {
			e.functions = map[string]api.Function{}
		}
		e.functions[name] = fn
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

// Encode encodes picture, as frame pts, a key frame when key is set, and
// returns what it gave.
func (e *Encoder) Encode(ctx context.Context, picture *image.YCbCr, pts int64, key bool) ([]Packet, error) {
	if picture.Rect.Dx() != e.config.Width || picture.Rect.Dy() != e.config.Height || picture.SubsampleRatio != image.YCbCrSubsampleRatio420 {
		return nil, fmt.Errorf("vp9: a picture of %v %v for an encoder of %dx%d", picture.Rect.Size(), picture.SubsampleRatio, e.config.Width, e.config.Height)
	}
	started := time.Now()
	w, h := e.config.Width, e.config.Height
	for plane, rows := range [][2]int{{w, h}, {w / 2, h / 2}, {w / 2, h / 2}} {
		src, stride := picture.Y, picture.YStride
		switch plane {
		case 1:
			src, stride = picture.Cb, picture.CStride
		case 2:
			src, stride = picture.Cr, picture.CStride
		}
		ptr, err := e.call(ctx, "vpxe_plane", e.handle, uint64(plane))
		if err != nil {
			return nil, err
		}
		dstStride, err := e.call(ctx, "vpxe_stride", e.handle, uint64(plane))
		if err != nil {
			return nil, err
		}
		if ptr == 0 || int(dstStride) < rows[0] {
			return nil, fmt.Errorf("vp9: no plane %d", plane)
		}
		for y := range rows[1] {
			if !e.module.Memory().Write(uint32(ptr)+uint32(y)*uint32(dstStride), src[y*stride:y*stride+rows[0]]) {
				return nil, fmt.Errorf("vp9: plane %d outside sandbox memory", plane)
			}
		}
	}
	return e.encode(ctx, started, pts, key, false)
}

// Flush ends the stream, returning what the encoder still held.
func (e *Encoder) Flush(ctx context.Context) ([]Packet, error) {
	return e.encode(ctx, time.Now(), -1, false, true)
}

func (e *Encoder) encode(ctx context.Context, started time.Time, pts int64, key, flush bool) ([]Packet, error) {
	keyArg, flushArg := uint64(0), uint64(0)
	if key {
		keyArg = 1
	}
	if flush {
		flushArg = 1
	}
	status, err := e.call(ctx, "vpxe_encode", e.handle, uint64(pts), keyArg, flushArg)
	if err != nil {
		return nil, err
	}
	if int32(status) != 0 {
		return nil, fmt.Errorf("vp9: encode failed with status %d", int32(status))
	}
	var out []Packet
	for {
		size, err := e.call(ctx, "vpxe_next", e.handle)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			break
		}
		ptr, err := e.call(ctx, "vpxe_data", e.handle)
		if err != nil {
			return nil, err
		}
		data, ok := e.module.Memory().Read(uint32(ptr), uint32(size))
		if !ok {
			return nil, fmt.Errorf("vp9: packet outside sandbox memory")
		}
		isKey, err := e.call(ctx, "vpxe_key", e.handle)
		if err != nil {
			return nil, err
		}
		at, err := e.call(ctx, "vpxe_pts", e.handle)
		if err != nil {
			return nil, err
		}
		out = append(out, Packet{Data: append([]byte(nil), data...), Key: isKey == 1, PTS: int64(at)})
	}
	if err := e.runtime.sandbox.Check(ctx, e.module, started); err != nil {
		return nil, fmt.Errorf("vp9: %w", err)
	}
	return out, nil
}

// Close ends the encoder and its sandbox.
func (e *Encoder) Close(ctx context.Context) {
	if e.module == nil {
		return
	}
	_, _ = e.call(ctx, "vpxe_drop", e.handle)
	_ = e.module.Close(ctx)
	e.module = nil
}

// AlphaEncoder encodes pictures with transparency as WebM keeps them, as
// FFmpeg's libvpx-vp9 does: the colours in one stream, the alpha as the luma
// of another, frame for frame and key frame for key frame.
type AlphaEncoder struct {
	color, alpha *Encoder
	yuv, mask    *image.YCbCr
}

// NewAlphaEncoder starts the two encoders.
func (r *EncoderRuntime) NewAlphaEncoder(ctx context.Context, config EncoderConfig) (*AlphaEncoder, error) {
	color, err := r.NewEncoder(ctx, config)
	if err != nil {
		return nil, err
	}
	alpha, err := r.NewEncoder(ctx, config)
	if err != nil {
		color.Close(ctx)
		return nil, err
	}
	rect := image.Rect(0, 0, config.Width, config.Height)
	mask := image.NewYCbCr(rect, image.YCbCrSubsampleRatio420)
	for i := range mask.Cb {
		mask.Cb[i], mask.Cr[i] = 128, 128
	}
	return &AlphaEncoder{color: color, alpha: alpha, yuv: image.NewYCbCr(rect, image.YCbCrSubsampleRatio420), mask: mask}, nil
}

// Encode encodes picture, premultiplied as image.RGBA is, as frame pts;
// colour and alpha come out for the same frames.
func (a *AlphaEncoder) Encode(ctx context.Context, picture *image.RGBA, pts int64, key bool) (color, alpha []Packet, err error) {
	if picture.Rect.Size() != a.yuv.Rect.Size() {
		return nil, nil, fmt.Errorf("vp9: a picture of %v for an encoder of %v", picture.Rect.Size(), a.yuv.Rect.Size())
	}
	RGBAToYUVA(picture, a.yuv, a.mask)
	if color, err = a.color.Encode(ctx, a.yuv, pts, key); err != nil {
		return nil, nil, err
	}
	if alpha, err = a.alpha.Encode(ctx, a.mask, pts, key); err != nil {
		return nil, nil, err
	}
	return color, alpha, nil
}

// Flush ends both streams.
func (a *AlphaEncoder) Flush(ctx context.Context) (color, alpha []Packet, err error) {
	if color, err = a.color.Flush(ctx); err != nil {
		return nil, nil, err
	}
	alpha, err = a.alpha.Flush(ctx)
	return color, alpha, err
}

// Close ends both encoders.
func (a *AlphaEncoder) Close(ctx context.Context) {
	a.color.Close(ctx)
	a.alpha.Close(ctx)
}

// RGBAToYUVA converts a premultiplied picture into full-range BT.601 I420,
// as image.YCbCr and JPEG have it (the encoder says so in the bitstream),
// and its alpha into the luma of mask, whose chroma it leaves. The colours
// are unpremultiplied first: a decoder blends the colour stream with the
// alpha itself. The chroma of a 2x2 block is that of its pixels weighted by
// their alpha, so that a transparent pixel does not tint its neighbours.
func RGBAToYUVA(src *image.RGBA, yuv, mask *image.YCbCr) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	for y := range h {
		row := src.Pix[y*src.Stride:]
		for x := range w {
			r, g, b, a := uint32(row[x*4]), uint32(row[x*4+1]), uint32(row[x*4+2]), uint32(row[x*4+3])
			if a > 0 && a < 255 {
				r, g, b = min(255, r*255/a), min(255, g*255/a), min(255, b*255/a)
			}
			luma, _, _ := color.RGBToYCbCr(uint8(r), uint8(g), uint8(b))
			yuv.Y[y*yuv.YStride+x] = luma
			mask.Y[y*mask.YStride+x] = uint8(a)
		}
	}
	for cy := range (h + 1) / 2 {
		for cx := range (w + 1) / 2 {
			var sr, sg, sb, sa uint32
			for dy := range 2 {
				for dx := range 2 {
					x, y := cx*2+dx, cy*2+dy
					if x >= w || y >= h {
						continue
					}
					// Premultiplied sums weigh each pixel by its alpha.
					p := src.Pix[y*src.Stride+x*4:]
					sr, sg, sb, sa = sr+uint32(p[0]), sg+uint32(p[1]), sb+uint32(p[2]), sa+uint32(p[3])
				}
			}
			var r, g, b uint32
			if sa > 0 {
				r, g, b = min(255, sr*255/sa), min(255, sg*255/sa), min(255, sb*255/sa)
			}
			_, cb, cr := color.RGBToYCbCr(uint8(r), uint8(g), uint8(b))
			i := cy*yuv.CStride + cx
			yuv.Cb[i], yuv.Cr[i] = cb, cr
		}
	}
}
