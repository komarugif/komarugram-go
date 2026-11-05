// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/program"
)

// Media is what ffprobe tells of a video to play whole: the size of its
// pictures, their rate, its length, and whether it has sound.
type Media struct {
	Info
	Duration time.Duration
	Sound    bool
	// AudioCodec is the name of its sound's codec, "" for none.
	AudioCodec string
	// ColorSpace and ColorRange are as its picture states them, "" for not.
	ColorSpace, ColorRange string
	// PixelAspect is how wide a pixel of its picture shows, its height being
	// 1, as turned; 0 for a square one.
	PixelAspect float64
}

// maxFPS bounds the rate pictures are decoded at: a file may state any.
const maxFPS = 60

// ProbeMedia reads what Media tells of the file at path, a path or a URL,
// with the ffprobe next to ffmpeg (the one found for "").
func ProbeMedia(ctx context.Context, path, ffmpeg string) (Media, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ffprobe := ResolveFFprobe(ffmpeg)
	if ffprobe == "" {
		return Media{}, errors.New("ffprobe is not installed")
	}
	args := append(LimitArgs(ProbeMemory),
		"-format_whitelist", "mov,matroska,webm,gif",
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height,r_frame_rate,sample_aspect_ratio,color_space,color_range:stream_side_data=rotation:stream_tags=rotate:format=duration",
		"-of", "json", path)
	out, err := outputLimited(program.CommandContext(ctx, ffprobe, args...), ProbeMemory)
	if err != nil {
		return Media{}, fmt.Errorf("ffprobe: %w", err)
	}
	var probed struct {
		Streams []struct {
			Type       string `json:"codec_type"`
			Codec      string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			FrameRate  string `json:"r_frame_rate"`
			Aspect     string `json:"sample_aspect_ratio"`
			ColorSpace string `json:"color_space"`
			ColorRange string `json:"color_range"`
			// The turn a phone records with, in the newer FFmpeg's place and
			// in the older's.
			SideData []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
			Tags struct {
				Rotate string `json:"rotate"`
			} `json:"tags"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probed); err != nil {
		return Media{}, err
	}
	var m Media
	picture := false
	for _, s := range probed.Streams {
		switch s.Type {
		case "audio":
			if !m.Sound {
				m.AudioCodec = s.Codec
			}
			m.Sound = true
		case "video":
			if picture {
				continue
			}
			picture = true
			if s.Width <= 0 || s.Height <= 0 || s.Width > 8192 || s.Height > 8192 {
				return Media{}, fmt.Errorf("video dimensions %dx%d exceed limit", s.Width, s.Height)
			}
			m.Width, m.Height, m.FPS = s.Width, s.Height, 30
			// FFmpeg turns the pictures as they are decoded: this is their
			// size then.
			rotation, _ := strconv.ParseFloat(s.Tags.Rotate, 64)
			for _, side := range s.SideData {
				if side.Rotation != 0 {
					rotation = side.Rotation
				}
			}
			m.ColorSpace, m.ColorRange = s.ColorSpace, s.ColorRange
			m.PixelAspect = pixelAspect(s.Aspect)
			if int(math.Abs(rotation))%180 == 90 {
				m.Width, m.Height = s.Height, s.Width
				if m.PixelAspect > 0 {
					m.PixelAspect = 1 / m.PixelAspect
				}
			}
			if num, den, ok := strings.Cut(s.FrameRate, "/"); ok {
				n, errN := strconv.ParseFloat(num, 64)
				d, errD := strconv.ParseFloat(den, 64)
				if errN == nil && errD == nil && d != 0 && n/d > 0 {
					m.FPS = min(n/d, maxFPS)
				}
			}
		}
	}
	if !picture {
		return Media{}, errors.New("no video stream")
	}
	if seconds, err := strconv.ParseFloat(probed.Format.Duration, 64); err == nil && seconds > 0 {
		m.Duration = time.Duration(seconds * float64(time.Second))
	}
	return m, nil
}

// Frames decodes the pictures of a video from a time on, at a constant
// rate, in an ffmpeg of its own, as fast as they are read: the reader sets
// the pace, and an ffmpeg nobody reads from waits on its pipe.
type Frames struct {
	cmd    interface{ Wait() error }
	limit  *limited
	stdout io.ReadCloser
	cancel context.CancelFunc
	stderr *strings.Builder
	size   image.Point
	fps    float64
	start  time.Duration
	n      int
	// free are pixels given back, for the frames to come.
	mu   sync.Mutex
	free [][]uint8
}

// NewFrames starts an ffmpeg decoding the file at path, whose pictures
// are of source, from start, its pictures scaled to size, fps a second.
// The ffmpeg may take DecodeMemory(source).
func NewFrames(path, ffmpeg string, start time.Duration, source, size image.Point, fps float64) (*Frames, error) {
	if size.X <= 0 || size.Y <= 0 || fps <= 0 {
		return nil, fmt.Errorf("frames of %v at %v fps", size, fps)
	}
	ctx, cancel := context.WithCancel(context.Background())
	memory := DecodeMemory(source)
	args := append(LimitArgs(memory), "-hide_banner", "-loglevel", "error", "-nostdin",
		"-threads", "2", "-format_whitelist", "mov,matroska,webm,gif")
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-i", path, "-an", "-sn", "-dn",
		// A picture every 1/fps, the n-th showing at start+n/fps: the fps
		// filter, which every FFmpeg has, where -vsync is gone from new
		// ones and -fps_mode is missing from old ones.
		"-vf", fmt.Sprintf("fps=%s,scale=%d:%d", strconv.FormatFloat(fps, 'f', 3, 64), size.X, size.Y),
	)
	args = append(args, rawOutputArgs("rgba")...)
	cmd := program.CommandContext(ctx, ResolveFFmpeg(ffmpeg), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr := new(strings.Builder)
	cmd.Stderr = stderr
	limit, err := startLimited(cmd, memory)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	return &Frames{cmd: cmd, limit: limit, stdout: stdout, cancel: cancel, stderr: stderr, size: size, fps: fps, start: start}, nil
}

// Next reads the next picture and the time it shows at; io.EOF at the end.
func (f *Frames) Next() (*image.RGBA, time.Duration, error) {
	n := f.size.X * f.size.Y * 4
	f.mu.Lock()
	var pix []uint8
	if k := len(f.free); k > 0 {
		pix, f.free = f.free[k-1], f.free[:k-1]
	}
	f.mu.Unlock()
	if len(pix) != n {
		pix = make([]uint8, n)
	}
	if _, err := io.ReadFull(f.stdout, pix); err != nil {
		if f.limit.exceeded.Load() {
			return nil, 0, f.limit.explain(err)
		}
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			if msg := strings.TrimSpace(f.stderr.String()); msg != "" && f.n == 0 {
				return nil, 0, fmt.Errorf("ffmpeg: %s", msg)
			}
			return nil, 0, io.EOF
		}
		return nil, 0, err
	}
	at := f.start + time.Duration(float64(f.n)/f.fps*float64(time.Second))
	f.n++
	return &image.RGBA{Pix: pix, Stride: f.size.X * 4, Rect: image.Rectangle{Max: f.size}}, at, nil
}

// Release gives back a picture Next gave that is drawn no more.
func (f *Frames) Release(im *image.RGBA) {
	if im == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.free) < 4 && len(im.Pix) == f.size.X*f.size.Y*4 {
		f.free = append(f.free, im.Pix)
	}
}

// Close stops the ffmpeg.
func (f *Frames) Close() {
	f.cancel()
	_ = f.stdout.Close()
	_ = f.cmd.Wait()
}

// SoundRate is the rate Sound puts its samples out at.
const SoundRate = 48000

// Sound decodes the sound of a video from a time on, as 16-bit stereo at
// SoundRate, little-endian, in an ffmpeg of its own, as fast as it is read.
type Sound struct {
	cmd    interface{ Wait() error }
	limit  *limited
	stdout io.ReadCloser
	cancel context.CancelFunc
	stderr *strings.Builder
	read   int64
}

// NewSound starts an ffmpeg decoding the sound of the file at path from
// start. The ffmpeg may take ProbeMemory.
func NewSound(path, ffmpeg string, start time.Duration) (*Sound, error) {
	ctx, cancel := context.WithCancel(context.Background())
	args := append(LimitArgs(ProbeMemory), "-hide_banner", "-loglevel", "error", "-nostdin",
		"-threads", "1", "-format_whitelist", "mov,matroska,webm")
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-i", path, "-vn", "-sn", "-dn",
		"-ac", "2", "-ar", strconv.Itoa(SoundRate), "-f", "s16le", "-")
	cmd := program.CommandContext(ctx, ResolveFFmpeg(ffmpeg), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr := new(strings.Builder)
	cmd.Stderr = stderr
	limit, err := startLimited(cmd, ProbeMemory)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	return &Sound{cmd: cmd, limit: limit, stdout: stdout, cancel: cancel, stderr: stderr}, nil
}

// Read reads samples, four bytes a frame; io.EOF at the end.
func (s *Sound) Read(p []byte) (int, error) {
	n, err := s.stdout.Read(p)
	s.read += int64(n)
	if err != nil && s.limit.exceeded.Load() {
		return n, s.limit.explain(err)
	}
	if errors.Is(err, io.EOF) && s.read == 0 {
		if msg := strings.TrimSpace(s.stderr.String()); msg != "" {
			return n, fmt.Errorf("ffmpeg: %s", msg)
		}
	}
	return n, err
}

// Close stops the ffmpeg.
func (s *Sound) Close() {
	s.cancel()
	_ = s.stdout.Close()
	_ = s.cmd.Wait()
}
