// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
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
	out, err := program.CommandContext(ctx, ffprobe,
		"-format_whitelist", "mov,matroska,webm",
		"-v", "error",
		"-show_entries", "stream=codec_type,width,height,r_frame_rate:format=duration",
		"-of", "json", path,
	).Output()
	if err != nil {
		return Media{}, fmt.Errorf("ffprobe: %w", err)
	}
	var probed struct {
		Streams []struct {
			Type      string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			FrameRate string `json:"r_frame_rate"`
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

// NewFrames starts an ffmpeg decoding the file at path from start, its
// pictures scaled to size, fps a second.
func NewFrames(path, ffmpeg string, start time.Duration, size image.Point, fps float64) (*Frames, error) {
	if size.X <= 0 || size.Y <= 0 || fps <= 0 {
		return nil, fmt.Errorf("frames of %v at %v fps", size, fps)
	}
	ctx, cancel := context.WithCancel(context.Background())
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-threads", "2", "-format_whitelist", "mov,matroska,webm,gif"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-i", path, "-an", "-sn", "-dn",
		// A picture every 1/fps, the n-th showing at start+n/fps: the fps
		// filter, which every FFmpeg has, where -vsync is gone from new
		// ones and -fps_mode is missing from old ones.
		"-vf", fmt.Sprintf("fps=%s,scale=%d:%d", strconv.FormatFloat(fps, 'f', 3, 64), size.X, size.Y),
		"-pix_fmt", "rgba", "-f", "rawvideo", "-")
	cmd := program.CommandContext(ctx, ResolveFFmpeg(ffmpeg), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr := new(strings.Builder)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	return &Frames{cmd: cmd, stdout: stdout, cancel: cancel, stderr: stderr, size: size, fps: fps, start: start}, nil
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
	stdout io.ReadCloser
	cancel context.CancelFunc
	stderr *strings.Builder
	read   int64
}

// NewSound starts an ffmpeg decoding the sound of the file at path from
// start.
func NewSound(path, ffmpeg string, start time.Duration) (*Sound, error) {
	ctx, cancel := context.WithCancel(context.Background())
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-threads", "1", "-format_whitelist", "mov,matroska,webm"}
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
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	return &Sound{cmd: cmd, stdout: stdout, cancel: cancel, stderr: stderr}, nil
}

// Read reads samples, four bytes a frame; io.EOF at the end.
func (s *Sound) Read(p []byte) (int, error) {
	n, err := s.stdout.Read(p)
	s.read += int64(n)
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
