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
	// source is the size of the file's pictures, which bounds the memory
	// its decoder may take, when Width and Height were scaled from it.
	source image.Point
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
	args := append(LimitArgs(ProbeMemory),
		"-format_whitelist", "mov,matroska,webm,gif",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,r_frame_rate",
		"-of", "json", path)
	out, err := outputLimited(program.CommandContext(ctx, ffprobe, args...), ProbeMemory)
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

	mu    sync.Mutex
	frame *image.RGBA
	// taken is set once Frame has given the frame away; free holds the
	// pixels of frames given back with Release, or never given away, for
	// the frames to come.
	taken      bool
	free       [][]uint8
	userPaused bool
	autoPaused bool
	// start is where the first pass plays from; base is where the pass
	// that plays started, and decoded counts its frames, for Position.
	start    time.Duration
	base     time.Duration
	decoded  int
	lastPoll time.Time
	resume   chan struct{}
	stopped  bool
	err      error
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
	info.source = image.Pt(info.Width, info.Height)
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

// NewPlayerAs prepares a player decoding frames as info gives them, as a
// player of the same file probed them, without probing it again.
func NewPlayerAs(path string, info Info, ffmpeg string) *Player {
	return &Player{
		path:     path,
		ffmpeg:   ResolveFFmpeg(ffmpeg),
		info:     info,
		lastPoll: time.Now(),
		resume:   make(chan struct{}),
	}
}

// Info returns the size and frame rate of the decoded frames.
func (p *Player) Info() Info { return p.info }

// StartAt makes the first pass of the video, before Start, play from at;
// the passes after it play from the start.
func (p *Player) StartAt(at time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.start = max(0, at)
}

// Position is the time in the video of the last frame decoded, counted at
// the frame rate of Info from where the pass that plays started: it grows
// past the video's length as the video loops.
func (p *Player) Position() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info.FPS <= 0 || p.decoded == 0 {
		return p.base
	}
	return p.base + time.Duration(float64(p.decoded-1)/p.info.FPS*float64(time.Second))
}

// Start spawns ffmpeg and begins decoding in a goroutine. The video loops
// in that one process until Stop is called; a start set with StartAt is
// played by a process of its own, which plays to the end once.
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
	start := p.start
	p.start, p.base, p.decoded = 0, start, 0
	p.mu.Unlock()
	source := p.info.source
	if source == (image.Point{}) {
		source = image.Pt(p.info.Width, p.info.Height)
	}
	memory := DecodeMemory(source)
	args := append(LimitArgs(memory),
		"-hide_banner", "-loglevel", "error",
		"-re",
		"-threads", "2", "-format_whitelist", "mov,matroska,webm,gif",
	)
	if start > 0 {
		// The first pass, from start to the end: the passes after it,
		// looped, start over from the start.
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	} else {
		// One process loops the input: a short GIF would start an ffmpeg
		// for every pass, several a second.
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args,
		"-i", p.path,
		"-an",
		"-vf", fmt.Sprintf("scale=%d:%d", p.info.Width, p.info.Height),
	)
	args = append(args, rawOutputArgs("rgba")...)
	cmd := program.CommandContext(ctx, p.ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	limit, err := startLimited(cmd, memory)
	if err != nil {
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

		// Gio caches uploaded textures by image identity, so each frame is
		// an image of its own, over pixels given back if there are any.
		frame := p.newFrame(frameSize)
		if _, err := io.ReadFull(stdout, frame.Pix[:frameSize]); err != nil {
			if limit.exceeded.Load() {
				return limit.explain(err)
			}
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
			// A frame nobody took is nobody's: its pixels serve again.
			if p.frame != nil && !p.taken {
				p.recycleLocked(p.frame.Pix)
			}
			p.frame, p.taken = frame, false
			p.decoded++
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
	if p.frame != nil {
		p.taken = true
	}
	return p.frame, p.err
}

// maxFreeFrames bounds the pixels a player keeps for the frames to come.
const maxFreeFrames = 4

// Release gives back a frame Frame returned, which the caller no longer
// shows and will not draw again: its pixels serve a frame to come. A frame
// not given back is simply the caller's.
func (p *Player) Release(frame *image.RGBA) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if frame == nil || frame == p.frame {
		return
	}
	p.recycleLocked(frame.Pix)
}

func (p *Player) recycleLocked(pix []uint8) {
	if len(pix) == p.info.Width*p.info.Height*4 && len(p.free) < maxFreeFrames {
		p.free = append(p.free, pix)
	}
}

// newFrame returns an image for the next frame, over given back pixels if
// there are any.
func (p *Player) newFrame(size int) *image.RGBA {
	rect := image.Rect(0, 0, p.info.Width, p.info.Height)
	p.mu.Lock()
	defer p.mu.Unlock()
	if n := len(p.free); n > 0 && len(p.free[n-1]) == size {
		pix := p.free[n-1]
		p.free = p.free[:n-1]
		return &image.RGBA{Pix: pix, Stride: p.info.Width * 4, Rect: rect}
	}
	return image.NewRGBA(rect)
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
