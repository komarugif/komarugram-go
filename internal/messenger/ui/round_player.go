// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"komarugram/internal/messenger/wasmmodule"
	"komarugram/pkg/audio"
	"komarugram/pkg/h264"
	"komarugram/pkg/mp4"
	"komarugram/pkg/resample"
)

// roundVideo is the picture of a video message that plays, or of a video
// that plays in the viewer: the H.264 track of its file, decoded in a
// WebAssembly sandbox of its own (pkg/h264), a picture at a time as the
// sound comes to it. The sound is the clock, as in Telegram Desktop: a
// pause, a move or another speed of the sound moves the picture with it.
type roundVideo struct {
	rt    *h264.Runtime
	video *h264.Video
	// box is the size the picture is drawn at, in pixels, its width over
	// its height: the pictures are scaled to cover it, a video message's,
	// or to fit in it, cover unset, and no larger than they are.
	box   atomic.Uint64
	cover bool
	// shownAt is the time of the picture to draw, in the video.
	shownAt atomic.Int64
	mu      sync.Mutex
	// picture is the last picture decoded, nil before the first; taken is
	// the one the view drew last, takenAt when. Neither one's pixels are
	// written over: pix are the pixels pictures are put in, in turn, three
	// at most: the picture to draw, the one the view may be drawing, and
	// the one being made.
	picture, taken *image.RGBA
	takenAt        time.Time
	pix            [][]uint8
}

// roundIdle is how long the pictures go on being decoded once the view
// stopped drawing them: a round scrolled away, or another chat, plays its
// sound only, and its picture catches up with the sound once drawn again.
const roundIdle = 500 * time.Millisecond

// openRound starts decoding the video of the file in data, fetching the
// decoder first if it is not there. Without one, offline say, the message
// goes to the external player: errFormat.
func openRound(ctx context.Context, data []byte) (*roundVideo, error) {
	module, err := wasmmodule.AVCDec.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	rt, err := h264.NewRuntime(ctx, module)
	if err != nil {
		return nil, err
	}
	video, err := rt.OpenVideo(ctx, data)
	if err != nil {
		rt.Close(context.Background())
		return nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	return &roundVideo{rt: rt, video: video, cover: true}, nil
}

// openVideoFile starts decoding the video of f, read where it lies as it
// plays, fetching the decoder first if it is not there. A file that is not
// H.264 in MP4, or without a decoder, goes to the external player:
// errFormat.
func openVideoFile(ctx context.Context, f *audioFile) (*roundVideo, error) {
	file, err := mp4.DemuxAt(f.r, f.size)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	module, err := wasmmodule.AVCDec.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	rt, err := h264.NewRuntime(ctx, module)
	if err != nil {
		return nil, err
	}
	video, err := rt.OpenFile(ctx, file)
	if err != nil {
		rt.Close(context.Background())
		return nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	return &roundVideo{rt: rt, video: video}, nil
}

// duration is how long the video plays.
func (r *roundVideo) duration() time.Duration { return r.video.File.Duration }

// close frees the decoder and the pictures; run must have returned.
func (r *roundVideo) close() {
	r.video.Close(context.Background())
	r.rt.Close(context.Background())
	r.mu.Lock()
	r.picture, r.taken, r.pix = nil, nil, nil
	r.mu.Unlock()
}

// take is the picture to draw, nil before the first, which the view draws
// at side pixels across.
func (r *roundVideo) take(now time.Time, side int) image.Image {
	return r.takeIn(now, image.Pt(side, side))
}

// takeIn is the picture to draw, nil before the first, which the view
// draws in box.
func (r *roundVideo) takeIn(now time.Time, box image.Point) image.Image {
	r.box.Store(uint64(uint32(box.X))<<32 | uint64(uint32(box.Y)))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.takenAt = now
	if r.picture == nil {
		return nil
	}
	r.taken = r.picture
	return r.picture
}

// drawn reports whether the view drew the pictures lately.
func (r *roundVideo) drawn(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.picture == nil || now.Sub(r.takenAt) < roundIdle
}

// show converts the picture decoded last into pixels the view does not
// use, and makes it the one to draw.
func (r *roundVideo) show() {
	size := image.Pt(r.video.File.Width, r.video.File.Height)
	b := r.box.Load()
	if box := image.Pt(int(int32(b>>32)), int(int32(b))); box.X > 0 && box.Y > 0 {
		if fit := resample.Fit(size.X, size.Y, box, r.cover); fit.X < size.X {
			size = fit
		}
	}
	n := size.X * size.Y * 4
	r.mu.Lock()
	var free []uint8
	for _, pix := range r.pix {
		if len(pix) == n && !usesPixels(r.picture, pix) && !usesPixels(r.taken, pix) {
			free = pix
			break
		}
	}
	if free == nil {
		// Pixels of another size, the round drawn larger or smaller now,
		// go once they are not used: at most two are.
		kept := r.pix[:0]
		for _, pix := range r.pix {
			if usesPixels(r.picture, pix) || usesPixels(r.taken, pix) {
				kept = append(kept, pix)
			}
		}
		free = make([]uint8, n)
		r.pix = append(kept, free)
	}
	r.mu.Unlock()
	// The view never draws these pixels while they are written: they are
	// neither the picture to draw nor the one it drew.
	im := r.video.PictureInto(&image.RGBA{Pix: free, Stride: size.X * 4, Rect: image.Rectangle{Max: size}})
	r.mu.Lock()
	r.picture = im
	r.mu.Unlock()
}

// usesPixels reports whether im lies over pix.
func usesPixels(im *image.RGBA, pix []uint8) bool {
	return im != nil && len(pix) > 0 && len(im.Pix) > 0 && &im.Pix[0] == &pix[0]
}

// run decodes the pictures as clock, where the sound is, comes to their
// times, and calls shown for each picture to draw, until ctx is done. A
// start past the beginning, or a move of the sound back or far ahead, starts
// from the keyframe before where the sound is. While the view does not draw
// the pictures, none is decoded.
func (r *roundVideo) run(ctx context.Context, clock func() time.Duration, shown func()) error {
	v := r.video
	// at is the index of the picture decoded last; -1 for none.
	at := -1
	for ctx.Err() == nil {
		now := time.Now()
		pos := clock()
		if !r.drawn(now) {
			if !sleep(ctx, 100*time.Millisecond) {
				break
			}
			continue
		}
		if at < 0 && pos > 0 || at >= 0 && (pos < v.Time(at)-100*time.Millisecond || pos > v.Time(at)+time.Second) {
			k, err := v.Seek(ctx, pos)
			if err != nil {
				return err
			}
			at = k - 1
		}
		decoded := false
		for at < 0 || at+1 < v.Pictures() && v.Time(at+1) <= pos {
			i, err := v.Next(ctx)
			if err != nil {
				return err
			}
			at, decoded = i, true
		}
		if decoded {
			r.show()
			r.shownAt.Store(int64(v.Time(at)))
			shown()
		}
		wait := 50 * time.Millisecond
		if at+1 < v.Pictures() {
			wait = min(max(v.Time(at+1)-pos, 2*time.Millisecond), 40*time.Millisecond)
		}
		if !sleep(ctx, wait) {
			break
		}
	}
	return nil
}

// sleep waits for d, and reports whether ctx is still going.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// silence is a sound of samples frames of nothing, the clock of a video
// message without a sound track.
type silence struct{ pos, samples int64 }

// silenceOf is silence as long as d.
func silenceOf(d time.Duration) *silence {
	return &silence{samples: max(1, int64(d)*audio.Rate/int64(time.Second))}
}

func (s *silence) Read(pcm []audio.Frame) (int, error) {
	n := int(min(int64(len(pcm)), s.samples-s.pos))
	if n <= 0 {
		return 0, io.EOF
	}
	clear(pcm[:n])
	s.pos += int64(n)
	return n, nil
}

func (s *silence) SeekSample(pos int64) error {
	s.pos = min(max(pos, 0), s.samples)
	return nil
}

func (s *silence) Position() int64 { return s.pos }
