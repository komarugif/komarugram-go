// SPDX-License-Identifier: Unlicense OR MIT

package h264

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sort"
	"time"

	"komarugram/pkg/mp4"
	"komarugram/pkg/resample"
)

// Video plays the H.264 track of an MP4 file in a loop, one picture at a
// time, in the order they show.
type Video struct {
	File    *mp4.File
	decoder *Decoder
	// next is the next sample to send; shown counts the pictures put out in
	// this loop; ended is set once the end of the stream was sent.
	next, shown int
	ended       bool
	picture     *image.YCbCr
	scaler      resample.VideoScaler
	// sample holds the sample sent last of a file read where it lies.
	sample []byte
}

// OpenVideo demuxes data and starts a decoder for it.
func (r *Runtime) OpenVideo(ctx context.Context, data []byte) (*Video, error) {
	file, err := mp4.Demux(data)
	if err != nil {
		return nil, err
	}
	return r.OpenFile(ctx, file)
}

// OpenFile starts a decoder for file, which may be read where it lies
// (mp4.DemuxAt): its samples are read as they are fed.
func (r *Runtime) OpenFile(ctx context.Context, file *mp4.File) (*Video, error) {
	if file.Width > r.maxSide || file.Height > r.maxSide {
		return nil, fmt.Errorf("h264: size %dx%d is over the limit of %d", file.Width, file.Height, r.maxSide)
	}
	decoder, err := r.NewDecoderOf(ctx, file.Codec, file.Config)
	if err != nil {
		return nil, err
	}
	return &Video{File: file, decoder: decoder}, nil
}

// Size is the size the container gives the pictures.
func (v *Video) Size() image.Point { return image.Pt(v.File.Width, v.File.Height) }

// Next decodes the next picture and returns its index in the loop, starting
// the loop again after its last picture. The picture is read with Picture.
func (v *Video) Next(ctx context.Context) (int, error) {
	for {
		picture, err := v.decoder.Receive(ctx)
		switch {
		case err == nil:
			v.picture = picture
			v.shown++
			return v.shown - 1, nil
		case errors.Is(err, errAgain):
			if err := v.feed(ctx); err != nil {
				return 0, err
			}
		case errors.Is(err, errEOF):
			if v.shown == 0 {
				return 0, fmt.Errorf("h264: the stream holds no pictures")
			}
			// The first sample is a keyframe: the loop starts over there.
			if err := v.decoder.Flush(ctx); err != nil {
				return 0, err
			}
			v.next, v.shown, v.ended = 0, 0, false
		default:
			return 0, err
		}
	}
}

// feed sends the decoder the next sample, or the end of the stream.
func (v *Video) feed(ctx context.Context) error {
	if v.next < v.File.Len() {
		sample, err := v.File.Sample(v.next, v.sample)
		if err != nil {
			return err
		}
		v.sample = sample
		v.next++
		err = v.decoder.Send(ctx, sample)
		var broken *StreamError
		switch {
		case err == nil || errors.As(err, &broken):
			// A broken sample is FFmpeg's to conceal.
			return nil
		case errors.Is(err, errAgain), errors.Is(err, errEOF):
			return fmt.Errorf("h264: the decoder takes no samples")
		}
		return err
	}
	if v.ended {
		return fmt.Errorf("h264: the decoder asks for samples after the end")
	}
	v.ended = true
	if err := v.decoder.Send(ctx, nil); err != nil && !errors.Is(err, errEOF) {
		return err
	}
	return nil
}

// Pictures is how many pictures one loop shows.
func (v *Video) Pictures() int { return len(v.File.Times) }

// Seek makes Next go on from the last keyframe that shows at t or before
// it, and returns the index Next gives that keyframe; the caller decodes on
// from there to the picture it wants. A keyframe is taken to be an IDR
// picture, as encoders put them: every picture sent before it shows before
// it, so that the k-th sample, a keyframe, is the k-th picture out. The
// picture decoded last stays until Next decodes another.
func (v *Video) Seek(ctx context.Context, t time.Duration) (int, error) {
	// The pictures that show at t or before it; the keyframe is the last
	// of them.
	shown := sort.Search(len(v.File.Times), func(i int) bool { return v.File.Times[i] > t })
	k := 0
	for i := min(shown, v.File.Len()) - 1; i > 0; i-- {
		if v.File.Keyframe(i) {
			k = i
			break
		}
	}
	if err := v.decoder.Flush(ctx); err != nil {
		return 0, err
	}
	v.next, v.shown, v.ended = k, k, false
	return k, nil
}

// Time is when picture i of the loop shows.
func (v *Video) Time(i int) time.Duration {
	if i < 0 || i >= len(v.File.Times) {
		return v.File.Duration
	}
	return v.File.Times[i]
}

// Picture returns the last picture decoded, scaled to size, as a new
// image. It never converts the picture at its own size when scaling down.
func (v *Video) Picture(size image.Point) *image.RGBA {
	if v.picture == nil {
		return nil
	}
	return v.scaler.Resize(v.picture, nil, size.X, size.Y)
}

// PictureInto is Picture scaled into dst, an image at the origin of the
// size to scale to, instead of a new image; nil before the first picture.
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
