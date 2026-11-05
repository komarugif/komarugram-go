// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/audio"
	"komarugram/pkg/opus"
	"komarugram/pkg/video"
	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

// opusCodec is the Matroska id of the sound the sandbox plays.
const opusCodec = "A_OPUS"

// openWebMVideo starts decoding the VP9 video of index. One over HD, as for the
// H.264 sandbox, or in another codec, goes to the external player: errFormat.
func openWebMVideo(ctx context.Context, index *webm.Index) (*roundVideo, error) {
	if !videoHD(index.Width, index.Height) {
		return nil, fmt.Errorf("%w: a WebM of %dx%d", errFormat, index.Width, index.Height)
	}
	rt, err := vp9.NewRuntimeWithLimits(ctx, vp9.VideoLimits)
	if err != nil {
		return nil, err
	}
	v, err := rt.OpenFile(ctx, index)
	if err != nil {
		rt.Close(context.Background())
		return nil, fmt.Errorf("%w: %w", errFormat, err)
	}
	return &roundVideo{rt: rt, video: v, index: index}, nil
}

// openWebMSound opens the sound of index: Opus decoded in its sandbox, read from
// where it lies as it plays, or silence as long as the video when it has none.
// Other sound, Vorbis say, goes to the external player: errFormat.
func openWebMSound(ctx context.Context, index *webm.Index, long time.Duration) (audio.Source, int64, error) {
	if index.AudioCodec == "" {
		s := silenceOf(long)
		return s, s.samples, nil
	}
	if index.AudioCodec != opusCodec {
		return nil, 0, fmt.Errorf("%w: sound in %s", errFormat, index.AudioCodec)
	}
	tocs := make([][2]byte, len(index.Audio))
	for i, u := range index.Audio {
		tocs[i] = u.TOC
	}
	stream, err := opus.NewStream(index.AudioHead, tocs, func(i int, buf []byte) ([]byte, error) {
		return index.Read(index.Audio[i].Unit, buf)
	})
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", errFormat, err)
	}
	rt, err := opus.NewRuntime(ctx)
	if err != nil {
		return nil, 0, err
	}
	// The sound outlives ctx's first use: it is read as long as it plays.
	reading := context.Background()
	d, err := rt.NewDecoder(reading)
	if err != nil {
		rt.Close(reading)
		return nil, 0, err
	}
	r, err := stream.NewReader(reading, d)
	if err != nil {
		rt.Close(reading)
		return nil, 0, err
	}
	return &webmSound{Reader: r, release: func() { rt.Close(reading) }}, stream.Samples(), nil
}

// webmLengthRead is how large a WebM has its blocks read for its length before
// FFmpeg plays it: the file's all downloaded for it.
const webmLengthRead = 64 << 20

// webmLength is media as FFmpeg told it, with the length of the blocks of a WebM of
// f, m's file, when they last longer: a file may say it lasts milliseconds, and FFmpeg
// believes it, and the sound that is the clock would end at once.
func webmLength(m model.Message, f *audioFile, media video.Media) video.Media {
	if m.Media.MIMEType != "video/webm" || f.size > webmLengthRead {
		return media
	}
	if index, err := webm.ReadIndex(f.r, f.size); err == nil && index.Duration > media.Duration {
		media.Duration = index.Duration
	}
	return media
}

// webmSound is the sound of a WebM, whose decoder close frees.
type webmSound struct {
	*opus.Reader
	release func()
}

func (s *webmSound) close() { s.release() }
