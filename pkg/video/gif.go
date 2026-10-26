// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"komarugram/pkg/program"
)

// A GIF of Telegram's is an H.264 MP4 without sound, which it plays looped
// and muted: at most HD, here within the box of 1280×720 turned as the
// video is, and within GIFMaxBytes, over which Telegram plays it as a
// video. The encoding is that of KomaruGram's GIF bot (video2gif): x264's
// slow preset at CRF 28, scaled, without the sound; a bitrate cap from the
// length keeps it within the size, and one too large still is encoded
// again under a lower one.

const (
	// GIFMaxBytes is the most a GIF may weigh.
	GIFMaxBytes = 10 << 20
	// gifLong and gifShort bound the long and the short side of a GIF.
	gifLong, gifShort = 1280, 720
	gifCRF            = 28
	// gifPasses is how many times a GIF too large is encoded.
	gifPasses = 3
	// gifFill is the share of GIFMaxBytes the bitrate cap aims at: the
	// container and the rate control's swings take the rest.
	gifFill = 0.9
	// gifEncodeMemory is what x264 may take besides decoding.
	gifEncodeMemory = 768 << 20
)

// ErrGIFTooLarge is a GIF that weighs more than GIFMaxBytes after every
// encoding.
var ErrGIFTooLarge = errors.New("GIF exceeds 10 MiB")

// GIFPlan is how a video is encoded as a GIF.
type GIFPlan struct {
	Width, Height int
	CRF           int
	Preset        string
	// MaxRate caps the bitrate, in bits a second; 0 for none.
	MaxRate int
}

// GIFSize is the size of a GIF of a video of width×height: the same, within
// the box of 1280×720 turned as the video is, even, as H.264 takes it.
func GIFSize(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	boxW, boxH := gifLong, gifShort
	if height > width {
		boxW, boxH = gifShort, gifLong
	}
	scale := min(1, float64(boxW)/float64(width), float64(boxH)/float64(height))
	even := func(v float64) int { return max(2, int(v/2+.5)*2) }
	return min(even(float64(width)*scale), boxW), min(even(float64(height)*scale), boxH)
}

// PlanGIF is the first encoding of a video of width×height lasting
// duration, unknown for 0.
func PlanGIF(width, height int, duration time.Duration) GIFPlan {
	w, h := GIFSize(width, height)
	p := GIFPlan{Width: w, Height: h, CRF: gifCRF, Preset: "slow"}
	if duration > 0 {
		p.MaxRate = int(GIFMaxBytes * 8 * gifFill / duration.Seconds())
	}
	return p
}

// Again is the encoding after one that gave size bytes, too many: under a
// cap lowered by as much as it was over, which also lowers the quality.
func (p GIFPlan) Again(size int64, duration time.Duration) GIFPlan {
	rate := p.MaxRate
	if rate <= 0 && duration > 0 {
		rate = int(float64(size) * 8 / duration.Seconds())
	}
	if rate > 0 && size > 0 {
		p.MaxRate = int(float64(rate) * GIFMaxBytes * gifFill / float64(size))
	}
	p.CRF = min(p.CRF+4, 40)
	return p
}

// Args are FFmpeg's arguments to encode the file at in, a path or a URL, as
// a GIF at out, telling its progress on the standard output.
func (p GIFPlan) Args(in, out string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-format_whitelist", "mov,matroska,webm,gif",
		"-i", in,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-map_metadata", "-1",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=lanczos,format=yuv420p", p.Width, p.Height),
		"-c:v", "libx264", "-preset", p.Preset, "-crf", strconv.Itoa(p.CRF),
	}
	if p.MaxRate > 0 {
		args = append(args, "-maxrate", strconv.Itoa(p.MaxRate), "-bufsize", strconv.Itoa(2*p.MaxRate))
	}
	return append(args, "-movflags", "+faststart", "-f", "mp4",
		"-progress", "pipe:1", "-nostats", out)
}

// GIFProgress is how far an encoding has come, as FFmpeg's -progress
// tells it.
type GIFProgress struct {
	// Pass is the encoding, from 1.
	Pass int
	// Done is the time of the video encoded, of Total.
	Done, Total time.Duration
	Frame       int
	Size        int64
	// End is set by the last report of an encoding.
	End bool
}

// Share is how much of the encoding is done, from 0 to 1; below 0 when the
// length is unknown.
func (p GIFProgress) Share() float64 {
	if p.End {
		return 1
	}
	if p.Total <= 0 {
		return -1
	}
	return min(1, max(0, p.Done.Seconds()/p.Total.Seconds()))
}

// ReadGIFProgress reads FFmpeg's -progress from r, blocks of key=value
// lines each ended by progress=, and calls report with each, at progress.
func ReadGIFProgress(r io.Reader, progress GIFProgress, report func(GIFProgress)) error {
	lines := bufio.NewScanner(r)
	for lines.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(lines.Text()), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "frame":
			if n, err := strconv.Atoi(value); err == nil {
				progress.Frame = n
			}
		case "total_size":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				progress.Size = n
			}
		case "out_time_us", "out_time_ms":
			// out_time_ms is in microseconds as well, by an old mistake
			// of FFmpeg's kept.
			if n, err := strconv.ParseInt(value, 10, 64); err == nil && n >= 0 {
				progress.Done = time.Duration(n) * time.Microsecond
			}
		case "progress":
			progress.End = value == "end"
			report(progress)
		}
	}
	return lines.Err()
}

// GIF is a GIF encoded.
type GIF struct {
	Width, Height int
	Duration      time.Duration
	Size          int64
}

// ConvertGIF encodes the video at in, a path or a URL, as a GIF at out with
// ffmpeg (the one found for ""), and tells report how far it has come; it
// is ErrGIFTooLarge when the GIF stays over GIFMaxBytes.
func ConvertGIF(ctx context.Context, ffmpeg, in, out string, report func(GIFProgress)) (GIF, error) {
	path := ResolveFFmpeg(ffmpeg)
	if path == "" {
		return GIF{}, errors.New("FFmpeg is not installed")
	}
	media, err := ProbeMedia(ctx, in, ffmpeg)
	if err != nil {
		return GIF{}, err
	}
	plan := PlanGIF(media.Width, media.Height, media.Duration)
	gif := GIF{Width: plan.Width, Height: plan.Height, Duration: media.Duration}
	memory := DecodeMemory(image.Pt(media.Width, media.Height)) + gifEncodeMemory
	for pass := 1; pass <= gifPasses; pass++ {
		if err := encodeGIF(ctx, path, plan.Args(in, out), memory, GIFProgress{Pass: pass, Total: media.Duration}, report); err != nil {
			return GIF{}, err
		}
		info, err := os.Stat(out)
		if err != nil {
			return GIF{}, err
		}
		if gif.Size = info.Size(); gif.Size <= GIFMaxBytes {
			return gif, nil
		}
		plan = plan.Again(gif.Size, media.Duration)
	}
	return GIF{}, ErrGIFTooLarge
}

// encodeGIF runs ffmpeg with args, its memory bounded, reading its
// progress.
func encodeGIF(ctx context.Context, ffmpeg string, args []string, memory uint64, progress GIFProgress, report func(GIFProgress)) error {
	cmd := program.CommandContext(ctx, ffmpeg, append(LimitArgs(memory), args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := new(strings.Builder)
	cmd.Stderr = stderr
	limit, err := startLimited(cmd, memory)
	if err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	report(progress)
	readErr := ReadGIFProgress(stdout, progress, report)
	// What is left unread lets FFmpeg end.
	_, _ = io.Copy(io.Discard, stdout)
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return fmt.Errorf("ffmpeg: %w", limit.explain(err))
	}
	return readErr
}
