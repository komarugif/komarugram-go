// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"komarugram/pkg/program"
	"strings"
	"time"
)

// Format is how cached frames are stored. Gio paints from *image.RGBA only:
// paint.NewImageOp converts anything else on the spot, and its shaders are
// compiled ahead of time, so there is no way to hand the GPU a compact frame
// and let a shader expand it. The choice is therefore what to keep in memory,
// and who pays for the conversion.
type Format int

const (
	// FormatRGBA keeps frames ready to paint at 4 bytes per pixel.
	FormatRGBA Format = iota

	// FormatYCbCr keeps 4:2:0 planes at 1.5 bytes per pixel, and converts a
	// frame to RGBA when it is painted. That is 2.7x less memory for about
	// 77 µs of CPU per painted 134x240 frame.
	FormatYCbCr
)

func (f Format) String() string {
	if f == FormatYCbCr {
		return "YCbCr 4:2:0"
	}
	return "RGBA"
}

// BytesPerPixel is what one pixel of a cached frame costs, times two for the
// chroma planes of a 4:2:0 frame.
func (f Format) bytesPerFrame(size image.Point) int {
	if f == FormatYCbCr {
		return size.X*size.Y + 2*(size.X/2)*(size.Y/2)
	}
	return size.X * size.Y * 4
}

func (f Format) pixFmt() string {
	if f == FormatYCbCr {
		return "yuv420p"
	}
	return "rgba"
}

// Clip is a short video decoded once and kept in memory as ready-to-paint
// frames. Playing it back costs nothing but picking a frame by the clock, so a
// grid of clips needs no running decoder at all.
type Clip struct {
	Name   string
	Size   image.Point
	FPS    float64
	Format Format

	frames []image.Image

	// scratch receives the converted frame for a compact clip. Reusing one
	// buffer is safe because a clip is painted once per frame, from the
	// layout goroutine.
	scratch *image.RGBA
}

// DecodeClip decodes path in one pass, scaled down to maxHeight and resampled
// to fps, storing the frames in the given format. At most maxFrames frames are
// kept; a longer video is truncated, which is what caps the memory a single
// clip can take.
func DecodeClip(path string, maxHeight int, fps float64, maxFrames int, format Format) (*Clip, error) {
	info, err := Probe(path)
	if err != nil {
		return nil, err
	}
	size := image.Point{X: info.Width, Y: info.Height}
	if maxHeight > 0 && size.Y > maxHeight {
		size.X = size.X * maxHeight / size.Y
		size.Y = maxHeight
	}
	size.X &^= 1
	size.Y &^= 1

	ffmpeg := ResolveFFmpeg("")
	if ffmpeg == "" {
		return nil, errors.New("ffmpeg is not installed")
	}
	// No -re here: the clip is read as fast as ffmpeg can decode it.
	cmd := program.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-threads", "1",
		"-i", path,
		"-an",
		// out_range=jpeg matters for the compact format: ffmpeg would
		// otherwise emit limited-range luma, while image.YCbCr decodes full
		// range and would wash the colours out.
		"-vf", fmt.Sprintf("fps=%g,scale=%d:%d:out_range=jpeg", fps, size.X, size.Y),
		"-pix_fmt", format.pixFmt(),
		"-f", "rawvideo", "-",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	frameSize := format.bytesPerFrame(size)
	clip := &Clip{
		Name:   path,
		Size:   size,
		FPS:    fps,
		Format: format,
	}
	if format == FormatYCbCr {
		clip.scratch = image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
	}
	// One backing buffer for the whole clip: the frames are contiguous, which
	// keeps allocation overhead and fragmentation down.
	pixels := make([]byte, 0, frameSize*min(maxFrames, 256))
	for len(clip.frames) < maxFrames {
		offset := len(pixels)
		pixels = append(pixels, make([]byte, frameSize)...)
		if _, err := io.ReadFull(stdout, pixels[offset:]); err != nil {
			pixels = pixels[:offset]
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, fmt.Errorf("ffmpeg: %s", msg)
			}
			return nil, err
		}
		clip.frames = append(clip.frames, newFrame(format, size, pixels[offset:offset+frameSize:offset+frameSize]))
	}
	if len(clip.frames) == 0 {
		return nil, fmt.Errorf("%s: no frames decoded", path)
	}
	return clip, nil
}

// newFrame wraps one raw frame straight out of the pipe, without copying.
func newFrame(format Format, size image.Point, pix []byte) image.Image {
	if format == FormatRGBA {
		return &image.RGBA{
			Pix:    pix,
			Stride: size.X * 4,
			Rect:   image.Rect(0, 0, size.X, size.Y),
		}
	}
	lumaSize := size.X * size.Y
	chromaSize := (size.X / 2) * (size.Y / 2)
	return &image.YCbCr{
		Y:              pix[:lumaSize:lumaSize],
		Cb:             pix[lumaSize : lumaSize+chromaSize : lumaSize+chromaSize],
		Cr:             pix[lumaSize+chromaSize:],
		YStride:        size.X,
		CStride:        size.X / 2,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, size.X, size.Y),
	}
}

// Len is how many frames the loop holds.
func (c *Clip) Len() int { return len(c.frames) }

// Bytes reports how much memory the cached frames take.
func (c *Clip) Bytes() int {
	return len(c.frames) * c.Format.bytesPerFrame(c.Size)
}

// Duration is how long one loop of the cached frames lasts.
func (c *Clip) Duration() time.Duration {
	return time.Duration(float64(len(c.frames)) / c.FPS * float64(time.Second))
}

// FrameAt picks the frame to show at elapsed, looping forever. A compact clip
// is converted into the clip's scratch buffer, so the result stays valid only
// until the next call for the same clip.
func (c *Clip) FrameAt(elapsed time.Duration) *image.RGBA {
	idx := int(elapsed.Seconds() * c.FPS)
	if idx < 0 {
		idx = 0
	}
	frame := c.frames[idx%len(c.frames)]
	if rgba, ok := frame.(*image.RGBA); ok {
		return rgba
	}
	draw.Draw(c.scratch, c.scratch.Rect, frame, image.Point{}, draw.Src)
	return c.scratch
}
