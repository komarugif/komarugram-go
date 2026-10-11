// SPDX-License-Identifier: Unlicense OR MIT

// Package video decodes an mp4 file with an external ffmpeg process and
// exposes the frames as image.RGBA values that can be painted by Gio.
package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"komarugram/pkg/program"
	"strconv"
	"strings"
	"sync"
	"time"
)

// idleTimeout is how long the player keeps decoding after the last Frame call.
// A page that is no longer on screen stops polling, and playback pauses itself
// instead of leaving an ffmpeg process running in the background.
const idleTimeout = time.Second

// Info describes the video stream of a media file.
type Info struct {
	Width  int
	Height int
	FPS    float64
}

// String describes the decoded stream, e.g. "404x720 · 29.97 fps".
func (i Info) String() string {
	return fmt.Sprintf("%dx%d · %.2f fps", i.Width, i.Height, i.FPS)
}

// Probe reads the dimensions and frame rate of the first video stream.
func Probe(path string) (Info, error) { return probe(path, "") }

func probe(path, ffmpeg string) (Info, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ffprobe := ResolveFFprobe(ffmpeg)
	if ffprobe == "" {
		return Info{}, errors.New("ffprobe is not installed")
	}
	out, err := program.CommandContext(ctx, ffprobe,
		"-format_whitelist", "mov,matroska,webm,gif",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,r_frame_rate",
		"-of", "json", path,
	).Output()
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}

	var probed struct {
		Streams []struct {
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			FrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probed); err != nil {
		return Info{}, err
	}
	if len(probed.Streams) == 0 {
		return Info{}, fmt.Errorf("%s: no video stream", path)
	}
	stream := probed.Streams[0]

	if stream.Width <= 0 || stream.Height <= 0 || stream.Width > 8192 || stream.Height > 8192 {
		return Info{}, fmt.Errorf("video dimensions exceed limit")
	}
	info := Info{Width: stream.Width, Height: stream.Height, FPS: 30}
	if num, den, ok := strings.Cut(stream.FrameRate, "/"); ok {
		n, errN := strconv.ParseFloat(num, 64)
		d, errD := strconv.ParseFloat(den, 64)
		if errN == nil && errD == nil && d != 0 {
			info.FPS = n / d
		}
	}
	return info, nil
}

// Player decodes a file frame by frame in the background. A player is started
// once and then polled from the layout loop with Frame; the caller is the one
// that keeps asking for new frames, so it decides when to redraw.
type Player struct {
	cancelDecode context.CancelFunc
	path         string
	ffmpeg       string
	info         Info

	mu         sync.Mutex
	frame      *image.RGBA
	userPaused bool
	autoPaused bool
	lastPoll   time.Time
	resume     chan struct{}
	stopped    bool
	err        error
}

// NewPlayer probes path and prepares a player scaled down to at most
// maxHeight pixels, keeping the aspect ratio. Nothing is decoded until Start.
func NewPlayer(path string, maxHeight int) (*Player, error) {
	return NewPlayerWithFFmpeg(path, maxHeight, "")
}

// NewPlayerWithFFmpeg uses a custom executable and its companion ffprobe.
func NewPlayerWithFFmpeg(path string, maxHeight int, ffmpeg string) (*Player, error) {
	info, err := probe(path, ffmpeg)
	if err != nil {
		return nil, err
	}
	if maxHeight > 0 && info.Height > maxHeight {
		info.Width = info.Width * maxHeight / info.Height
		info.Height = maxHeight
	}
	if maxHeight > 0 && info.Width > maxHeight*2 {
		info.Height = max(2, info.Height*maxHeight*2/info.Width)
		info.Width = maxHeight * 2
	}
	// ffmpeg's scaler needs even dimensions for most pixel formats.
	info.Width = max(2, info.Width&^1)
	info.Height = max(2, info.Height&^1)

	return &Player{
		path:     path,
		ffmpeg:   ResolveFFmpeg(ffmpeg),
		info:     info,
		lastPoll: time.Now(),
		resume:   make(chan struct{}),
	}, nil
}

// NewPlayerOfSize prepares a player decoding frames of size, as a player of
// the same file probed them, without probing it again. Its Info has no frame
// rate.
func NewPlayerOfSize(path string, size image.Point, ffmpeg string) *Player {
	return &Player{
		path:     path,
		ffmpeg:   ResolveFFmpeg(ffmpeg),
		info:     Info{Width: size.X, Height: size.Y},
		lastPoll: time.Now(),
		resume:   make(chan struct{}),
	}
}

// Info returns the size and frame rate of the decoded frames.
func (p *Player) Info() Info { return p.info }

// Start spawns ffmpeg and begins decoding in a goroutine. The video loops
// in that one process until Stop is called.
func (p *Player) Start() {
	go p.watchIdle()
	go func() {
		for {
			err := p.decode()
			p.mu.Lock()
			stopped := p.stopped
			if err != nil && !stopped {
				p.err = err
			}
			p.mu.Unlock()
			if err != nil || stopped {
				return
			}
		}
	}()
}

// decode plays the file in a loop, returning nil if the stream ends anyway.
func (p *Player) decode() error {
	// -re makes ffmpeg emit frames at the native frame rate, so the pipe
	// itself paces playback and we do not need a ticker here.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil
	}
	p.cancelDecode = cancel
	p.mu.Unlock()
	cmd := program.CommandContext(ctx, p.ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-re",
		"-threads", "2", "-format_whitelist", "mov,matroska,webm,gif",
		// One process loops the input: a short GIF would start an ffmpeg
		// for every pass, several a second.
		"-stream_loop", "-1",
		"-i", p.path,
		"-an",
		"-vf", fmt.Sprintf("scale=%d:%d", p.info.Width, p.info.Height),
		"-pix_fmt", "rgba",
		"-f", "rawvideo", "-",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	frameSize := p.info.Width * p.info.Height * 4
	for {
		if stop := p.waitWhilePaused(); stop {
			return nil
		}

		// Gio caches uploaded textures by image identity, so each frame gets
		// its own buffer instead of being decoded in place.
		frame := image.NewRGBA(image.Rect(0, 0, p.info.Width, p.info.Height))
		if _, err := io.ReadFull(stdout, frame.Pix[:frameSize]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil // the stream ended: the caller starts it again
			}
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return fmt.Errorf("ffmpeg: %s", msg)
			}
			return err
		}

		p.mu.Lock()
		stopped := p.stopped
		if !stopped {
			p.frame = frame
		}
		p.mu.Unlock()
		if stopped {
			return nil
		}
	}
}

// waitWhilePaused blocks until playback resumes, reporting whether the player
// was stopped instead.
func (p *Player) waitWhilePaused() bool {
	for {
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return true
		}
		if !p.userPaused && !p.autoPaused {
			p.mu.Unlock()
			return false
		}
		resume := p.resume
		p.mu.Unlock()
		<-resume
	}
}

// watchIdle pauses playback once nobody has asked for a frame for a while.
func (p *Player) watchIdle() {
	ticker := time.NewTicker(idleTimeout / 2)
	defer ticker.Stop()
	for range ticker.C {
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return
		}
		if !p.autoPaused && time.Since(p.lastPoll) > idleTimeout {
			p.autoPaused = true
		}
		p.mu.Unlock()
	}
}

// Frame returns the most recently decoded frame, or nil before the first one
// arrives, together with the first decoding error if there was one. Calling it
// also keeps the player awake, so it must be called from every frame that
// shows the video.
func (p *Player) Frame() (*image.RGBA, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastPoll = time.Now()
	if p.autoPaused {
		p.autoPaused = false
		p.wakeLocked()
	}
	return p.frame, p.err
}

// SetPaused pauses or resumes playback. While paused the pipe fills up and
// ffmpeg blocks on its own, so no frames are lost.
func (p *Player) SetPaused(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.userPaused == paused || p.stopped {
		return
	}
	p.userPaused = paused
	if !paused {
		p.wakeLocked()
	}
}

// Paused reports whether playback was paused by the user. An idle player that
// paused itself still counts as playing: it resumes as soon as it is shown.
func (p *Player) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.userPaused
}

// Stop terminates decoding. The player cannot be restarted afterwards.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.stopped = true
	if p.cancelDecode != nil {
		p.cancelDecode()
	}
	p.userPaused = false
	p.autoPaused = false
	p.wakeLocked()
}

// wakeLocked releases a decoder goroutine waiting in waitWhilePaused. The
// caller must hold p.mu.
func (p *Player) wakeLocked() {
	close(p.resume)
	p.resume = make(chan struct{})
}
