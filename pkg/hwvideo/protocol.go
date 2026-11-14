// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"encoding/binary"
	"fmt"
	"io"
)

// The window and the helper talk over the helper's standard input and
// output. A message is its length in 4 bytes, little-endian, then the
// number of the request in 4, a byte, the operation of a request or the
// status of its answer, and a body; a request has the number of its stream
// in 4 bytes before the body. Requests are answered in any order; those of
// one stream are sent one at a time.

// Operations.
const (
	// opOpen starts a stream: the matrix of its colors, whether their
	// range is limited, and its decoder configuration. The answer is the
	// size of its pictures, width and height in 4 bytes each.
	opOpen = iota + 1
	// opSend decodes a sample, or, with no body, ends the stream.
	opSend
	// opReceive takes the next picture put out: a byte, 1 if there was one.
	opReceive
	// opRender scales the picture taken to a width and height, in 4 bytes
	// each, roughly if the byte after them is 1: the answer is its RGBA.
	opRender
	// opFlush forgets the pictures of a stream, to go on from a keyframe.
	opFlush
	// opClose ends a stream.
	opClose
)

// Statuses.
const (
	statusOK = iota
	// statusUnsupported: the stream is not decoded in hardware; the body
	// tells why.
	statusUnsupported
	// statusNoDevice: nothing is, on this machine.
	statusNoDevice
	// statusError: the stream failed.
	statusError
)

// The longest messages: a sample of a GIF, and a picture of the largest
// size rendered.
const (
	maxRequest  = 32 << 20
	maxRender   = 4096
	maxResponse = maxRender*maxRender*4 + 64
)

// message is a request or its answer.
type message struct {
	id     uint32
	kind   uint8
	stream uint32 // of a request
	body   []byte
}

// writeMessage writes m, the number of its stream only for a request.
func writeMessage(w io.Writer, m message, request bool) error {
	head := make([]byte, 4, 13)
	head = binary.LittleEndian.AppendUint32(head, m.id)
	head = append(head, m.kind)
	if request {
		head = binary.LittleEndian.AppendUint32(head, m.stream)
	}
	binary.LittleEndian.PutUint32(head, uint32(len(head)-4+len(m.body)))
	if _, err := w.Write(head); err != nil {
		return err
	}
	_, err := w.Write(m.body)
	return err
}

// readMessage reads a message of at most limit bytes.
func readMessage(r io.Reader, request bool, limit int) (message, error) {
	var m message
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return m, err
	}
	n := int(binary.LittleEndian.Uint32(size[:]))
	head := 5
	if request {
		head = 9
	}
	if n < head || n > limit {
		return m, fmt.Errorf("hwvideo: message of %d bytes", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return m, err
	}
	m.id, m.kind = binary.LittleEndian.Uint32(data), data[4]
	if request {
		m.stream = binary.LittleEndian.Uint32(data[5:])
	}
	m.body = data[head:]
	return m, nil
}
