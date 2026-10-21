// SPDX-License-Identifier: Unlicense OR MIT

// Package tgswebm turns a Lottie sticker (TGS) into a video sticker: a WebM
// of VP9 with alpha, as Telegram's video stickers are, which other programs
// play, where a TGS plays only in Telegram. The animation is drawn by
// pkg/lottie and encoded by pkg/vp9, each in its sandbox.
package tgswebm

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"time"

	"komarugram/pkg/lottie"
	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

// Options are what Convert makes. The zero value is Telegram's video
// sticker: 512 pixels a side, at most 30 frames a second, within 256 KiB
// when it can be.
type Options struct {
	Size   int
	MaxFPS int
	// MaxBytes is the size the file should keep within: it is encoded
	// again, at a lower quality, while it is larger, down to the lowest
	// quality tried.
	MaxBytes int
	// Speed is libvpx's (0 slowest, 9 fastest): 6 by default, the slowest
	// of its real time mode, which encodes a sticker of 3 s in about 3.5 s
	// here where its good quality (5) takes 13, for files about a quarter
	// larger at one quality.
	Speed int
}

func (o Options) withDefaults() Options {
	if o.Size <= 0 {
		o.Size = 512
	}
	if o.MaxFPS <= 0 {
		o.MaxFPS = 30
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 256 << 10
	}
	if o.Speed <= 0 {
		o.Speed = 6
	}
	return o
}

// qualities are the CQ levels tried, the best first.
var qualities = []int{30, 38, 46, 54}

// Convert draws the animation data, a TGS or Lottie JSON, and encodes it.
func Convert(ctx context.Context, data []byte, options Options) ([]byte, error) {
	o := options.withDefaults()
	if o.Size%2 != 0 {
		return nil, fmt.Errorf("tgswebm: a side of %d pixels", o.Size)
	}
	lr, err := lottie.NewRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer lr.Close(ctx)
	anim, err := lr.Open(ctx, "sticker", data, o.Size)
	if err != nil {
		return nil, err
	}
	defer anim.Close(ctx)
	if anim.FrameCount <= 0 || anim.FPS <= 0 {
		return nil, errors.New("tgswebm: an animation without frames")
	}
	fps := min(o.MaxFPS, int(math.Round(anim.FPS)))
	length := time.Duration(float64(anim.FrameCount) / anim.FPS * float64(time.Second))
	frames := max(1, int(math.Round(length.Seconds()*float64(fps))))
	// The pictures are drawn once, and encoded at each quality tried.
	pictures := make([]*image.RGBA, frames)
	for i := range pictures {
		pictures[i] = image.NewRGBA(image.Rectangle{Max: anim.Size})
		elapsed := time.Duration(i) * time.Second / time.Duration(fps)
		if _, err := anim.FrameInto(ctx, elapsed, pictures[i]); err != nil {
			return nil, err
		}
	}
	er, err := vp9.NewEncoderRuntime(ctx, vp9.EncoderLimits)
	if err != nil {
		return nil, err
	}
	defer er.Close(ctx)
	var out []byte
	for _, cq := range qualities {
		if out, err = encode(ctx, er, pictures, anim.Size, fps, cq, o.Speed); err != nil {
			return nil, err
		}
		if len(out) <= o.MaxBytes {
			break
		}
	}
	return out, nil
}

// encode encodes pictures at quality cq, a key frame every two seconds.
func encode(ctx context.Context, er *vp9.EncoderRuntime, pictures []*image.RGBA, size image.Point, fps, cq, speed int) ([]byte, error) {
	enc, err := er.NewAlphaEncoder(ctx, vp9.EncoderConfig{Width: size.X, Height: size.Y, FPS: fps, CQ: cq, Speed: speed})
	if err != nil {
		return nil, err
	}
	defer enc.Close(ctx)
	frameDuration := time.Second / time.Duration(fps)
	var frames []webm.MuxFrame
	take := func(color, alpha []vp9.Packet) error {
		if len(color) != len(alpha) {
			return fmt.Errorf("tgswebm: %d frames of colour, %d of alpha", len(color), len(alpha))
		}
		for i := range color {
			frames = append(frames, webm.MuxFrame{Data: color[i].Data, Alpha: alpha[i].Data, Key: color[i].Key,
				Timestamp: time.Duration(color[i].PTS) * frameDuration})
		}
		return nil
	}
	for i, p := range pictures {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		color, alpha, err := enc.Encode(ctx, p, int64(i), i%(2*fps) == 0)
		if err != nil {
			return nil, err
		}
		if err := take(color, alpha); err != nil {
			return nil, err
		}
	}
	color, alpha, err := enc.Flush(ctx)
	if err != nil {
		return nil, err
	}
	if err := take(color, alpha); err != nil {
		return nil, err
	}
	return webm.Mux(size.X, size.Y, frameDuration, frames)
}
