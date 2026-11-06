// SPDX-License-Identifier: Unlicense OR MIT

package opus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"
)

// Stream is the Opus stream of an OGG file (RFC 7845): its packets and
// where each begins, in samples at 48 kHz.
type Stream struct {
	// preSkip is how many samples the decoder puts out before the sound
	// starts: the encoder's delay.
	preSkip int64
	packets [][]byte
	// starts[i] is the sample packet i begins at, counted from the first
	// the decoder puts out; starts has one more entry, the end.
	starts []int64
	// read, when set, reads packet i into buf, grown as needed, for packets that
	// lie in a file: see NewStream.
	read func(i int, buf []byte) ([]byte, error)
	// length is the samples of sound, without preSkip and the padding the
	// last page's position cuts off.
	length int64
}

// ErrFormat is data that is not an OGG file of Opus.
var ErrFormat = errors.New("opus: not an OGG file of Opus")

// maxLength bounds a stream to twelve hours: the samples of a hostile file
// must not overflow what the player adds up.
const maxLength = 12 * 3600 * Rate

// Parse reads the Opus stream of the OGG file in data. The packets are
// slices of data, or copies of it when one spans pages.
func Parse(data []byte) (*Stream, error) {
	var (
		serial    uint32
		haveHead  bool
		packets   [][]byte
		partial   []byte
		continued bool
		granule   int64 = -1
		s               = &Stream{}
	)
	for pos := 0; pos < len(data); {
		if len(data)-pos < 27 || !bytes.Equal(data[pos:pos+4], []byte("OggS")) || data[pos+4] != 0 {
			if len(packets) > 0 {
				break // Junk after the stream.
			}
			return nil, ErrFormat
		}
		page := data[pos:]
		pageSerial := binary.LittleEndian.Uint32(page[14:])
		segments := int(page[26])
		if len(page) < 27+segments {
			return nil, ErrFormat
		}
		lacing := page[27 : 27+segments]
		body := 27 + segments
		size := 0
		for _, n := range lacing {
			size += int(n)
		}
		if len(page) < body+size {
			// A file cut short: what came whole plays.
			break
		}
		pos += body + size
		if len(packets) == 0 && !haveHead && partial == nil {
			serial = pageSerial
		} else if pageSerial != serial {
			continue // Another logical stream.
		}
		if page[5]&1 == 0 {
			// Not a continuation: a packet left open is lost.
			partial, continued = nil, false
		}
		at := body
		for _, n := range lacing {
			seg := page[at : at+int(n)]
			at += int(n)
			var packet []byte
			switch {
			case continued:
				partial = append(partial, seg...)
			case n == 255:
				partial = append([]byte(nil), seg...)
			default:
				packet = seg
			}
			continued = n == 255
			if n < 255 {
				if partial != nil {
					packet, partial = partial, nil
				}
				if !haveHead {
					if err := s.head(packet); err != nil {
						return nil, err
					}
					haveHead = true
				} else if len(packets) == 0 && bytes.HasPrefix(packet, []byte("OpusTags")) {
					// Comments: nothing to play.
				} else if len(packet) > 0 {
					packets = append(packets, packet)
				}
			}
		}
		if g := int64(binary.LittleEndian.Uint64(page[6:])); g >= 0 && len(packets) > 0 {
			granule = g
		}
	}
	if !haveHead || len(packets) == 0 {
		return nil, ErrFormat
	}
	s.packets = packets
	s.starts = make([]int64, len(packets)+1)
	for i, p := range packets {
		s.starts[i+1] = s.starts[i] + int64(samples(p))
		if s.starts[i+1] > maxLength {
			return nil, errors.New("opus: stream too long")
		}
	}
	s.length = s.starts[len(packets)] - s.preSkip
	// The last page's position ends the sound; the samples past it are the
	// encoder's padding.
	if granule >= 0 && granule-s.preSkip < s.length {
		s.length = granule - s.preSkip
	}
	if s.length <= 0 {
		return nil, errors.New("opus: no sound")
	}
	return s, nil
}

// NewStream is the Opus stream of packets kept elsewhere, as those of a WebM file:
// head is its OpusHead, tocs the first two bytes of each packet, which say how long
// it plays, and read reads packet i into buf, grown as needed. A packet is read when
// it is played.
func NewStream(head []byte, tocs [][2]byte, read func(i int, buf []byte) ([]byte, error)) (*Stream, error) {
	s := &Stream{read: read}
	if err := s.head(head); err != nil {
		return nil, err
	}
	if len(tocs) == 0 {
		return nil, ErrFormat
	}
	s.starts = make([]int64, len(tocs)+1)
	for i, toc := range tocs {
		s.starts[i+1] = s.starts[i] + int64(samples(toc[:]))
		if s.starts[i+1] > maxLength {
			return nil, errors.New("opus: stream too long")
		}
	}
	s.length = s.starts[len(tocs)] - s.preSkip
	if s.length <= 0 {
		return nil, errors.New("opus: no sound")
	}
	return s, nil
}

// count is how many packets the stream has.
func (s *Stream) count() int { return len(s.starts) - 1 }

// packet is packet i, read into buf when it lies elsewhere.
func (s *Stream) packet(i int, buf []byte) ([]byte, error) {
	if s.read != nil {
		return s.read(i, buf)
	}
	return s.packets[i], nil
}

// head reads the OpusHead packet.
func (s *Stream) head(p []byte) error {
	if len(p) < 19 || !bytes.HasPrefix(p, []byte("OpusHead")) || p[8]>>4 != 0 {
		return ErrFormat
	}
	channels, family := p[9], p[18]
	// Families other than 0 and 1 carry more than one Opus stream per
	// packet, which a voice message never has.
	if channels == 0 || family > 1 || family == 1 && channels > 2 {
		return errors.New("opus: multichannel streams are not supported")
	}
	s.preSkip = int64(binary.LittleEndian.Uint16(p[10:]))
	return nil
}

// Duration is how long the stream plays.
func (s *Stream) Duration() time.Duration {
	return time.Duration(s.length) * time.Second / Rate
}

// Samples is how many samples the stream plays, at 48 kHz.
func (s *Stream) Samples() int64 { return s.length }

// samples is how many samples at 48 kHz packet p decodes to, from its TOC
// byte (RFC 6716, 3.1); 0 for a packet too short to tell.
func samples(p []byte) int {
	if len(p) == 0 {
		return 0
	}
	config := int(p[0] >> 3)
	var frame int // in samples at 48 kHz
	switch {
	case config < 12: // SILK: 10, 20, 40, 60 ms.
		frame = []int{480, 960, 1920, 2880}[config%4]
	case config < 16: // Hybrid: 10, 20 ms.
		frame = []int{480, 960}[config%2]
	default: // CELT: 2.5, 5, 10, 20 ms.
		frame = []int{120, 240, 480, 960}[config%4]
	}
	frames := 1
	switch p[0] & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(p) < 2 {
			return 0
		}
		frames = int(p[1] & 0x3f)
	}
	return min(frames*frame, maxSamples)
}
