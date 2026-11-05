// SPDX-License-Identifier: Unlicense OR MIT

package vp9

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sort"
	"time"

	"komarugram/pkg/resample"
	"komarugram/pkg/sandbox"
	"komarugram/pkg/webm"
)

// VideoLimits suit a video in HD or a little over: a 1280×720 stream keeps some
// 40 MiB in its decoder.
var VideoLimits = Limits{
	Limits:  sandbox.Limits{Memory: 192 << 20, Slow: time.Second},
	MaxSide: 1920,
}

// PictureLimits suit one picture of a video of any size, as its thumbnail: nothing
// is kept for the next one, and it may take its time.
var PictureLimits = Limits{
	Limits:  sandbox.Limits{Memory: 256 << 20, Slow: 10 * time.Second},
	MaxSide: 8192,
}

// ErrCodec is a video in a codec other than VP9: VP8 or AV1 say.
var ErrCodec = errors.New("vp9: the video is not VP9")

// Video plays the VP9 track of a WebM file read where it lies, one picture at a
// time: its frames show in the order they lie, each block one picture.
type Video struct {
	index   *webm.Index
	decoder *Decoder
	// next is the next frame to decode.
	next    int
	picture *image.YCbCr
	scaler  resample.VideoScaler
	sample  []byte
}

// OpenFile starts a decoder for the video of index.
func (r *Runtime) OpenFile(ctx context.Context, index *webm.Index) (*Video, error) {
	if index.CodecID != CodecID {
		return nil, fmt.Errorf("%w: %s", ErrCodec, index.CodecID)
	}
	if index.Width > r.maxSide || index.Height > r.maxSide {
		return nil, fmt.Errorf("vp9: size %dx%d is over the limit of %d", index.Width, index.Height, r.maxSide)
	}
	decoder, err := r.NewDecoder(ctx)
	if err != nil {
		return nil, err
	}
	return &Video{index: index, decoder: decoder}, nil
}

// Size is the size it shows at.
func (v *Video) Size() image.Point { return image.Pt(v.index.Width, v.index.Height) }

// Duration is how long it lasts.
func (v *Video) Duration() time.Duration { return v.index.Duration }

// Pictures is how many pictures it shows.
func (v *Video) Pictures() int { return len(v.index.Video) }

// Time is when picture i shows.
func (v *Video) Time(i int) time.Duration {
	if i < 0 || i >= len(v.index.Times) {
		return v.index.Duration
	}
	return v.index.Times[i]
}

// Next decodes the next picture and returns its index, from the first again after
// the last. The picture is read with PictureInto or Picture.
func (v *Video) Next(ctx context.Context) (int, error) {
	if v.next >= len(v.index.Video) {
		v.next = 0
	}
	sample, err := v.index.Read(v.index.Video[v.next], v.sample)
	if err != nil {
		return 0, err
	}
	v.sample = sample
	if colors, ok := packetColors(sample, v.index.Width, v.index.Height); ok {
		v.scaler.Colors = colors
	}
	picture, err := v.decoder.Decode(ctx, sample)
	if err != nil {
		return 0, err
	}
	v.picture = picture
	v.next++
	return v.next - 1, nil
}

// Seek makes Next go on from the last keyframe that shows at t or before it, and
// returns the index Next gives it. The picture decoded last stays until Next
// decodes another.
func (v *Video) Seek(_ context.Context, t time.Duration) (int, error) {
	shown := sort.Search(len(v.index.Times), func(i int) bool { return v.index.Times[i] > t })
	k := 0
	for i := min(shown, len(v.index.Video)) - 1; i > 0; i-- {
		if v.index.Video[i].Key {
			k = i
			break
		}
	}
	v.next = k
	return k, nil
}

// Picture is the last picture decoded, scaled to size, as a new image.
func (v *Video) Picture(size image.Point) *image.RGBA {
	if v.picture == nil {
		return nil
	}
	return v.scaler.Resize(v.picture, nil, size.X, size.Y)
}

// PictureInto is Picture scaled into dst, an image at the origin of the size to
// scale to.
func (v *Video) PictureInto(dst *image.RGBA) *image.RGBA {
	if v.picture == nil {
		return nil
	}
	return v.scaler.ResizeInto(dst, v.picture, nil)
}

// Close releases the decoder's sandbox.
func (v *Video) Close(ctx context.Context) {
	v.picture = nil
	v.decoder.Close(ctx)
}
