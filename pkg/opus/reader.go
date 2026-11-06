// SPDX-License-Identifier: Unlicense OR MIT

package opus

import (
	"context"
	"io"
	"sort"

	"komarugram/pkg/audio"
)

// preRoll is how much a decoder is given before the point it starts at, as
// RFC 7845 recommends: it converges after 80 ms.
const preRoll = Rate * 80 / 1000

// Reader decodes a stream from any point of it, on demand, into 48 kHz
// frames, the mono sound in both channels: a voice message costs its packets and one decoder, whatever its
// length, and moving through it decodes a few packets.
type Reader struct {
	ctx     context.Context
	stream  *Stream
	decoder *Decoder
	// pos is the next sample Read puts out, from the start of the sound.
	pos int64
	// next is the packet decoded next; skip is how many of the samples to
	// come fall before pos.
	next int
	skip int64
	// ready are samples decoded and not put out yet.
	ready []int16
	// buf holds the packet decoded last, of a stream read as it plays.
	buf []byte
}

// NewReader reads s with d, from the start.
func (s *Stream) NewReader(ctx context.Context, d *Decoder) (*Reader, error) {
	r := &Reader{ctx: ctx, stream: s, decoder: d}
	return r, r.SeekSample(0)
}

// Position is the next sample Read puts out, from the start of the sound.
func (r *Reader) Position() int64 { return r.pos }

// SeekSample moves to sample pos of the sound, within 0 and the stream's end.
func (r *Reader) SeekSample(pos int64) error {
	pos = max(0, min(pos, r.stream.length))
	target := pos + r.stream.preSkip
	// The first packet to decode begins at least preRoll before target.
	from := max(0, target-preRoll)
	k := sort.Search(r.stream.count(), func(i int) bool { return r.stream.starts[i+1] > from })
	k = min(k, r.stream.count()-1)
	if err := r.decoder.Reset(r.ctx); err != nil {
		return err
	}
	r.pos, r.next, r.skip, r.ready = pos, k, target-r.stream.starts[k], nil
	return nil
}

// Read fills out with frames, and returns io.EOF at the end of the sound.
func (r *Reader) Read(out []audio.Frame) (int, error) {
	n := 0
	for n < len(out) {
		if left := r.stream.length - r.pos; left <= 0 {
			if n == 0 {
				return 0, io.EOF
			}
			break
		}
		if len(r.ready) == 0 {
			if r.next >= r.stream.count() {
				// The packets ran out before the end the pages told.
				r.pos = r.stream.length
				continue
			}
			packet, err := r.stream.packet(r.next, r.buf)
			if err != nil {
				return n, err
			}
			r.buf = packet
			pcm, err := r.decoder.Decode(r.ctx, packet)
			if err != nil {
				return n, err
			}
			r.next++
			drop := min(r.skip, int64(len(pcm)))
			r.skip -= drop
			r.ready = pcm[drop:]
			continue
		}
		m := int(min(int64(len(out)-n), int64(len(r.ready)), r.stream.length-r.pos))
		for i, v := range r.ready[:m] {
			out[n+i] = audio.Dual(v)
		}
		r.ready = r.ready[m:]
		r.pos += int64(m)
		n += m
	}
	return n, nil
}
