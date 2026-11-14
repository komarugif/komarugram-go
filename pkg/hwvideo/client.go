// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"komarugram/pkg/mp4"
	"komarugram/pkg/program"
	"komarugram/pkg/resample"
)

// ErrHelper is the error of everything asked of a helper that ended on
// its own: a fault of a driver, most likely. Nothing is decoded in
// hardware after it.
var ErrHelper = errors.New("hwvideo: the decoding process ended")

// idleStop is how long the helper lives with no stream to decode.
const idleStop = 10 * time.Second

// Client decodes videos in a helper, a process of its own it starts when
// a video is opened and stops once none has been open for a while.
type Client struct {
	// command makes the command that starts the helper.
	command func() *exec.Cmd

	mu      sync.Mutex
	helper  *helper
	streams int
	stream  uint32
	idle    *time.Timer
	// opened counts the videos opened.
	opened int
	// failed is why nothing is decoded in hardware any more: the helper
	// ended on its own, or the machine has no decoder.
	failed error
}

// NewClient makes a client whose helper command starts; nil for the
// client's own binary with Flag.
func NewClient(command func() *exec.Cmd) *Client {
	if command == nil {
		command = func() *exec.Cmd {
			self, err := os.Executable()
			if err != nil {
				self = os.Args[0]
			}
			return program.Command(self, "-"+Flag)
		}
	}
	return &Client{command: command}
}

// Opened is how many videos the helper has opened so far.
func (c *Client) Opened() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opened
}

// Failed is why the client decodes nothing any more, nil while it does.
func (c *Client) Failed() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed
}

// helper is a running helper.
type helper struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	inMu  sync.Mutex
	mu    sync.Mutex
	next  uint32
	calls map[uint32]chan message
	// err is why the helper answers no more, once it does not.
	err error
	// stopped is set when the client stopped it itself.
	stopped bool
}

// start starts the helper; c.mu is held.
func (c *Client) start() (*helper, error) {
	cmd := c.command()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	h := &helper{cmd: cmd, in: in, calls: map[uint32]chan message{}}
	go func() {
		r := bufio.NewReaderSize(out, 1<<16)
		var err error
		for {
			var m message
			if m, err = readMessage(r, false, maxResponse); err != nil {
				break
			}
			h.mu.Lock()
			call := h.calls[m.id]
			delete(h.calls, m.id)
			h.mu.Unlock()
			if call != nil {
				call <- m
			}
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		h.mu.Lock()
		h.err = fmt.Errorf("%w: %v", ErrHelper, err)
		stopped := h.stopped
		h.mu.Unlock()
		if !stopped {
			log.Printf("hwvideo: the decoding process ended on its own (%v): videos are decoded in software from now on", cmd.ProcessState)
			c.mu.Lock()
			if c.helper == h {
				c.helper = nil
			}
			if c.failed == nil {
				c.failed = h.err
			}
			c.mu.Unlock()
		}
		// Those waiting are told last: the client has failed by then.
		h.mu.Lock()
		for id, call := range h.calls {
			close(call)
			delete(h.calls, id)
		}
		h.mu.Unlock()
	}()
	return h, nil
}

// stop ends the helper: its input closed, it ends itself.
func (h *helper) stop() {
	h.mu.Lock()
	h.stopped = true
	h.mu.Unlock()
	h.inMu.Lock()
	h.in.Close()
	h.inMu.Unlock()
}

// call asks the helper for kind of stream and waits for its answer.
func (h *helper) call(kind uint8, stream uint32, body []byte) ([]byte, error) {
	answer := make(chan message, 1)
	h.mu.Lock()
	if h.err != nil {
		h.mu.Unlock()
		return nil, h.err
	}
	h.next++
	id := h.next
	h.calls[id] = answer
	h.mu.Unlock()
	h.inMu.Lock()
	err := writeMessage(h.in, message{id: id, kind: kind, stream: stream, body: body}, true)
	h.inMu.Unlock()
	if err != nil {
		h.mu.Lock()
		delete(h.calls, id)
		h.mu.Unlock()
		return nil, fmt.Errorf("%w: %v", ErrHelper, err)
	}
	m, ok := <-answer
	if !ok {
		h.mu.Lock()
		defer h.mu.Unlock()
		return nil, h.err
	}
	switch m.kind {
	case statusOK:
		return m.body, nil
	case statusUnsupported:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, m.body)
	case statusNoDevice:
		return nil, fmt.Errorf("%w: %s", errNoDevice, m.body)
	}
	return nil, fmt.Errorf("hwvideo: %s", m.body)
}

// Video plays the H.264 track of an MP4 file in a loop, one picture at a
// time in the order they show, as h264.Video does, decoded by the helper.
type Video struct {
	File   *mp4.File
	client *Client
	helper *helper
	id     uint32
	// next is the next sample to send; shown counts the pictures put out
	// in this loop; ended is set once the end of the stream was sent.
	next, shown int
	ended       bool
	rough       bool
	sample      []byte
	closed      bool
	// err is why a picture could not be rendered.
	err error
}

// Open starts decoding file in the helper, started if it is not running.
// It fails with ErrUnsupported for a file not decoded in hardware, and
// with the client's failure once it decodes nothing any more.
func (c *Client) Open(file *mp4.File) (*Video, error) {
	if file.Codec != mp4.H264 {
		return nil, unsupported("not H.264")
	}
	if file.Width > MaxSide || file.Height > MaxSide {
		return nil, unsupported("%dx%d", file.Width, file.Height)
	}
	c.mu.Lock()
	if c.failed != nil {
		defer c.mu.Unlock()
		return nil, c.failed
	}
	if c.helper == nil {
		h, err := c.start()
		if err != nil {
			c.failed = fmt.Errorf("%w: %v", ErrHelper, err)
			c.mu.Unlock()
			return nil, c.failed
		}
		c.helper = h
	}
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
	h := c.helper
	c.stream++
	id := c.stream
	c.streams++
	c.mu.Unlock()

	colors := resample.Colors{Limited: !file.FullRange}
	switch file.Matrix {
	case 1, 7:
		colors.Matrix = resample.BT709
	case 4, 5, 6:
		colors.Matrix = resample.BT601
	case 9, 10:
		colors.Matrix = resample.BT2020
	default:
		colors.Matrix = resample.GuessMatrix(file.Width, file.Height)
	}
	body := []byte{byte(colors.Matrix), 0}
	if colors.Limited {
		body[1] = 1
	}
	if _, err := h.call(opOpen, id, append(body, file.Config...)); err != nil {
		if errors.Is(err, errNoDevice) {
			c.mu.Lock()
			if c.failed == nil {
				c.failed = err
			}
			c.mu.Unlock()
		}
		c.closed()
		return nil, err
	}
	c.mu.Lock()
	c.opened++
	c.mu.Unlock()
	return &Video{File: file, client: c, helper: h, id: id}, nil
}

// closed counts a stream that ended, and stops the helper once none has
// been open for idleStop.
func (c *Client) closed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streams--
	if c.streams > 0 || c.helper == nil {
		return
	}
	if c.failed != nil {
		// A machine without a decoder needs no helper.
		c.helper.stop()
		c.helper = nil
		return
	}
	h := c.helper
	c.idle = time.AfterFunc(idleStop, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.streams == 0 && c.helper == h {
			h.stop()
			c.helper = nil
		}
	})
}

// Close stops the helper at once.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
	if c.helper != nil {
		c.helper.stop()
		c.helper = nil
	}
}

// Size is the size the container gives the pictures.
func (v *Video) Size() image.Point { return image.Pt(v.File.Width, v.File.Height) }

// Pictures is how many pictures one loop shows.
func (v *Video) Pictures() int { return len(v.File.Times) }

// Time is when picture i of the loop shows.
func (v *Video) Time(i int) time.Duration {
	if i < 0 || i >= len(v.File.Times) {
		return v.File.Duration
	}
	return v.File.Times[i]
}

// Next decodes the next picture and returns its index in the loop,
// starting the loop again after its last picture. The picture is read with
// Picture.
func (v *Video) Next(ctx context.Context) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		got, err := v.helper.call(opReceive, v.id, nil)
		if err != nil {
			return 0, err
		}
		if len(got) == 1 && got[0] == 1 {
			v.shown++
			return v.shown - 1, nil
		}
		switch {
		case v.next < v.File.Len():
			sample, err := v.File.Sample(v.next, v.sample)
			if err != nil {
				return 0, err
			}
			v.sample = sample
			v.next++
			if len(sample) == 0 {
				continue
			}
			if _, err := v.helper.call(opSend, v.id, sample); err != nil {
				return 0, err
			}
		case !v.ended:
			v.ended = true
			if _, err := v.helper.call(opSend, v.id, nil); err != nil {
				return 0, err
			}
		case v.shown == 0:
			return 0, fmt.Errorf("hwvideo: the stream holds no pictures")
		default:
			// The first sample is a keyframe: the loop starts over there.
			if _, err := v.helper.call(opFlush, v.id, nil); err != nil {
				return 0, err
			}
			v.next, v.shown, v.ended = 0, 0, false
		}
	}
}

// Seek makes Next go on from the last keyframe that shows at t or before
// it, and returns the index Next gives that keyframe, as h264.Video.Seek
// does.
func (v *Video) Seek(ctx context.Context, t time.Duration) (int, error) {
	shown := sort.Search(len(v.File.Times), func(i int) bool { return v.File.Times[i] > t })
	k := 0
	for i := min(shown, v.File.Len()) - 1; i > 0; i-- {
		if v.File.Keyframe(i) {
			k = i
			break
		}
	}
	if _, err := v.helper.call(opFlush, v.id, nil); err != nil {
		return 0, err
	}
	v.next, v.shown, v.ended = k, k, false
	return k, nil
}

// SetFast scales the pictures to come with the cheaper filter, as
// h264.Video.SetFast does; the hardware decodes them all the same.
func (v *Video) SetFast(ctx context.Context, fast bool) error {
	v.rough = fast
	return nil
}

// Picture returns the last picture decoded, scaled to size, as a new
// image; nil if it could not be had, Err telling why.
func (v *Video) Picture(size image.Point) *image.RGBA {
	if size.X <= 0 || size.Y <= 0 {
		return nil
	}
	return v.PictureInto(image.NewRGBA(image.Rectangle{Max: size}))
}

// PictureInto is Picture scaled into dst, an image at the origin of the
// size to scale to.
func (v *Video) PictureInto(dst *image.RGBA) *image.RGBA {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	body := binary.LittleEndian.AppendUint32(nil, uint32(w))
	body = binary.LittleEndian.AppendUint32(body, uint32(h))
	if v.rough {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	pix, err := v.helper.call(opRender, v.id, body)
	if err == nil && len(pix) != w*h*4 {
		err = fmt.Errorf("hwvideo: a picture of %d bytes for %dx%d", len(pix), w, h)
	}
	if err != nil {
		v.err = err
		return nil
	}
	for y := range h {
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+w*4], pix[y*w*4:])
	}
	return dst
}

// Err is why Picture last gave none.
func (v *Video) Err() error { return v.err }

// Close ends the stream in the helper.
func (v *Video) Close(ctx context.Context) {
	if v.closed {
		return
	}
	v.closed = true
	_, _ = v.helper.call(opClose, v.id, nil)
	v.client.closed()
}
