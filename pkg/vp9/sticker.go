// SPDX-License-Identifier: Unlicense OR MIT

package vp9

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"time"

	"komarugram/pkg/resample"
	"komarugram/pkg/webm"
)

// Sticker plays a WebM sticker: the container is demuxed in Go, the colour and
// alpha streams are decoded in two sandboxes, and the result is composed into
// an image Gio can paint.
type Sticker struct {
	Name string
	File *webm.File
	Size image.Point

	colour *Decoder
	alpha  *Decoder
	// planes and mask are frame index as decoded, in the decoders' buffers;
	// mask is nil for an opaque frame.
	planes, mask *image.YCbCr
	// index is the frame decoded last; -1 before the first one.
	index int
	// frame is composed at the sticker's size for FrameAt, which allocates
	// it; composed is the index it holds.
	frame    *image.RGBA
	composed int
	scaler   resample.VideoScaler
}

// OpenSticker demuxes data and prepares its streams for playback.
func (r *Runtime) OpenSticker(ctx context.Context, name string, data []byte) (*Sticker, error) {
	file, err := webm.Demux(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if !Supported(file) {
		return nil, fmt.Errorf("%s: codec %q is not VP9", name, file.CodecID)
	}
	// The composed frame is allocated here, outside the sandbox, at the size
	// the container claims; the claim is checked before anything is allocated.
	if file.Width <= 0 || file.Height <= 0 || file.Width > r.maxSide || file.Height > r.maxSide {
		return nil, fmt.Errorf("%s: size %dx%d is over the limit of %d", name, file.Width, file.Height, r.maxSide)
	}

	sticker := &Sticker{
		Name:     name,
		File:     file,
		Size:     image.Pt(file.Width, file.Height),
		index:    -1,
		composed: -1,
	}
	if sticker.colour, err = r.NewDecoder(ctx); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if file.HasAlpha() {
		if sticker.alpha, err = r.NewDecoder(ctx); err != nil {
			sticker.colour.Close(ctx)
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return sticker, nil
}

// HasAlpha reports whether this sticker carries transparency.
func (s *Sticker) HasAlpha() bool { return s.alpha != nil }

// FrameCount is how many frames one loop holds.
func (s *Sticker) FrameCount() int { return len(s.File.Frames) }

// FrameAt returns the frame belonging at elapsed, looping forever, at the
// sticker's size. The image is reused by the next call.
func (s *Sticker) FrameAt(ctx context.Context, elapsed time.Duration) (*image.RGBA, error) {
	if err := s.seek(ctx, elapsed); err != nil {
		return nil, err
	}
	if s.composed != s.index {
		if s.frame == nil {
			s.frame = image.NewRGBA(image.Rectangle{Max: s.Size})
		}
		draw.Draw(s.frame, s.frame.Rect, s.planes, image.Point{}, draw.Src)
		if s.mask != nil {
			applyAlpha(s.frame, s.mask)
		}
		s.composed = s.index
	}
	return s.frame, nil
}

// FrameSized returns the frame belonging at elapsed, looping forever, scaled
// to size, as a new image. Scaled down, it never composes the frame at the
// sticker's own size: a grid cell is a small part of it.
func (s *Sticker) FrameSized(ctx context.Context, elapsed time.Duration, size image.Point) (*image.RGBA, error) {
	if size == s.Size {
		frame, err := s.FrameAt(ctx, elapsed)
		if err != nil {
			return nil, err
		}
		out := image.NewRGBA(frame.Rect)
		copy(out.Pix, frame.Pix)
		return out, nil
	}
	if err := s.seek(ctx, elapsed); err != nil {
		return nil, err
	}
	return s.scaler.Resize(s.planes, s.mask, size.X, size.Y), nil
}

// FrameSizedInto is FrameSized into dst, an image at the origin of the size
// to scale to, instead of a new image; it returns dst.
func (s *Sticker) FrameSizedInto(ctx context.Context, elapsed time.Duration, dst *image.RGBA) (*image.RGBA, error) {
	if dst.Rect.Size() == s.Size {
		frame, err := s.FrameAt(ctx, elapsed)
		if err != nil {
			return nil, err
		}
		copy(dst.Pix, frame.Pix)
		return dst, nil
	}
	if err := s.seek(ctx, elapsed); err != nil {
		return nil, err
	}
	return s.scaler.ResizeInto(dst, s.planes, s.mask), nil
}

// seek decodes up to the frame belonging at elapsed. VP9 frames depend on
// their predecessors, so this decodes forward; wrapping around restarts from
// frame zero, which is a keyframe. Only the frame reached is composed, by
// the caller.
func (s *Sticker) seek(ctx context.Context, elapsed time.Duration) error {
	frames := len(s.File.Frames)
	if frames == 0 {
		return fmt.Errorf("%s: no frames", s.Name)
	}
	target := 0
	if s.File.FrameDuration > 0 {
		target = int(elapsed/s.File.FrameDuration) % frames
	}
	if target == s.index {
		return nil
	}
	start := s.index + 1
	if target < s.index || s.index < 0 {
		start = 0
	}
	for i := start; i <= target; i++ {
		if err := s.decode(ctx, i); err != nil {
			// The planes are those of no frame now.
			s.index, s.composed = -1, -1
			return err
		}
	}
	s.index = target
	return nil
}

// decode decodes frame i of both streams into planes and mask.
func (s *Sticker) decode(ctx context.Context, i int) error {
	frame := s.File.Frames[i]
	colour, err := s.colour.Decode(ctx, frame.Data)
	if err != nil {
		return fmt.Errorf("%s: colour frame %d: %w", s.Name, i, err)
	}
	s.planes, s.mask = colour, nil
	if s.alpha == nil || len(frame.Alpha) == 0 {
		return nil
	}
	mask, err := s.alpha.Decode(ctx, frame.Alpha)
	if err != nil {
		return fmt.Errorf("%s: alpha frame %d: %w", s.Name, i, err)
	}
	s.mask = mask
	return nil
}

// applyAlpha folds the alpha stream's luma into the colour frame. Gio paints
// premultiplied images, so the colours are scaled as the alpha is applied.
func applyAlpha(dst *image.RGBA, mask *image.YCbCr) {
	width := min(dst.Rect.Dx(), mask.Rect.Dx())
	height := min(dst.Rect.Dy(), mask.Rect.Dy())
	for y := range height {
		row := dst.Pix[y*dst.Stride:]
		luma := mask.Y[y*mask.YStride:]
		for x := range width {
			alpha := uint32(luma[x])
			pixel := row[x*4 : x*4+4 : x*4+4]
			if alpha == 0xff {
				pixel[3] = 0xff
				continue
			}
			pixel[0] = byte(uint32(pixel[0]) * alpha / 0xff)
			pixel[1] = byte(uint32(pixel[1]) * alpha / 0xff)
			pixel[2] = byte(uint32(pixel[2]) * alpha / 0xff)
			pixel[3] = byte(alpha)
		}
	}
}

// Close releases both sandboxes.
func (s *Sticker) Close(ctx context.Context) {
	// The planes lie in the decoders' sandbox memory.
	s.planes, s.mask, s.index = nil, nil, -1
	if s.colour != nil {
		s.colour.Close(ctx)
		s.colour = nil
	}
	if s.alpha != nil {
		s.alpha.Close(ctx)
		s.alpha = nil
	}
}
