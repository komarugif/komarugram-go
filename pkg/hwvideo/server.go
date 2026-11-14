// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"runtime/debug"
	"sync"

	"komarugram/pkg/resample"
)

// Flag is the flag the client's binary is started with to be the helper.
const Flag = "video-decoder"

// MaxStreams bounds the streams the helper decodes at once: a list of
// GIFs shows fewer.
const MaxStreams = 48

// stream is one stream of the helper.
type stream struct {
	mu      sync.Mutex
	decoder *Decoder
	scaler  resample.VideoScaler
	// picture is the picture taken last, read from its surface once it is
	// first rendered: one never shown is never read.
	picture image.YCbCr
	taken   bool
	read    bool
}

// server is the helper: the device and the streams decoded on it.
type server struct {
	newDevice func() (device, error)
	out       *bufio.Writer
	outMu     sync.Mutex
	mu        sync.Mutex
	device    device
	deviceErr error
	streams   map[uint32]*stream
}

// Serve is the helper: it decodes the streams asked for on r and answers
// on w, until r ends.
func Serve(r io.Reader, w io.Writer) error {
	return serve(r, w, newDevice)
}

func serve(r io.Reader, w io.Writer, newDevice func() (device, error)) error {
	s := &server{newDevice: newDevice, out: bufio.NewWriterSize(w, 1<<16), streams: map[uint32]*stream{}}
	in := bufio.NewReaderSize(r, 1<<16)
	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, st := range s.streams {
			st.decoder.Close()
		}
		if s.device != nil {
			s.device.close()
		}
	}()
	for {
		m, err := readMessage(in, true, maxRequest)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, body := s.handle(m)
			s.outMu.Lock()
			defer s.outMu.Unlock()
			if writeMessage(s.out, message{id: m.id, kind: status, body: body}, false) == nil {
				s.out.Flush()
			}
		}()
	}
}

// handle answers a request. A panic is the failure of the stream, not the
// end of the helper: the other streams go on.
func (s *server) handle(m message) (status uint8, body []byte) {
	defer func() {
		if v := recover(); v != nil {
			status, body = statusError, fmt.Appendf(nil, "panic: %v\n%s", v, debug.Stack())
		}
	}()
	body, err := s.do(m)
	if errors.Is(err, errDeviceLost) {
		// The GPU is gone for every stream: the client sees the helper end.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	switch {
	case err == nil:
		return statusOK, body
	case errors.Is(err, errNoDevice):
		return statusNoDevice, []byte(err.Error())
	case errors.Is(err, ErrUnsupported):
		return statusUnsupported, []byte(err.Error())
	}
	return statusError, []byte(err.Error())
}

// errNoDevice is the error of a machine with no hardware decoder.
var errNoDevice = errors.New("hwvideo: no hardware decoder")

func (s *server) do(m message) ([]byte, error) {
	if m.kind == opOpen {
		return s.open(m)
	}
	s.mu.Lock()
	st := s.streams[m.stream]
	if m.kind == opClose {
		delete(s.streams, m.stream)
	}
	s.mu.Unlock()
	if st == nil {
		return nil, malformed("no stream %d", m.stream)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	switch m.kind {
	case opSend:
		if len(m.body) == 0 {
			st.decoder.End()
			return nil, nil
		}
		return nil, st.decoder.Send(m.body)
	case opReceive:
		st.taken, st.read = st.decoder.Receive(), false
		if st.taken {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case opRender:
		if len(m.body) != 9 {
			return nil, malformed("render request of %d bytes", len(m.body))
		}
		w, h := int(binary.LittleEndian.Uint32(m.body)), int(binary.LittleEndian.Uint32(m.body[4:]))
		if w <= 0 || h <= 0 || w > maxRender || h > maxRender {
			return nil, malformed("picture rendered to %dx%d", w, h)
		}
		if !st.taken {
			return nil, malformed("no picture taken")
		}
		if !st.read {
			if err := st.decoder.Read(&st.picture); err != nil {
				return nil, err
			}
			st.read = true
		}
		st.scaler.Rough = m.body[8] == 1
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		st.scaler.ResizeInto(dst, &st.picture, nil)
		return dst.Pix, nil
	case opFlush:
		st.decoder.Flush()
		st.taken = false
		return nil, nil
	case opClose:
		st.decoder.Close()
		return nil, nil
	}
	return nil, malformed("operation %d", m.kind)
}

func (s *server) open(m message) ([]byte, error) {
	if len(m.body) < 2 {
		return nil, malformed("open request of %d bytes", len(m.body))
	}
	s.mu.Lock()
	if s.device == nil && s.deviceErr == nil {
		s.device, s.deviceErr = s.newDevice()
	}
	dev, err := s.device, s.deviceErr
	_, taken := s.streams[m.stream]
	count := len(s.streams)
	s.mu.Unlock()
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: %v", errNoDevice, err)
	case taken:
		return nil, malformed("stream %d is open", m.stream)
	case count >= MaxStreams:
		return nil, unsupported("%d streams at once", count)
	}
	decoder, err := newDecoder(dev, m.body[2:])
	if err != nil {
		return nil, err
	}
	st := &stream{decoder: decoder}
	st.scaler.Colors = resample.Colors{Matrix: resample.Matrix(m.body[0]), Limited: m.body[1] == 1}
	s.mu.Lock()
	s.streams[m.stream] = st
	s.mu.Unlock()
	size := decoder.Size()
	out := binary.LittleEndian.AppendUint32(nil, uint32(size.X))
	return binary.LittleEndian.AppendUint32(out, uint32(size.Y)), nil
}
