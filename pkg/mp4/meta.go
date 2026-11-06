// SPDX-License-Identifier: Unlicense OR MIT

package mp4

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// ErrNoVideo is returned for an MP4 file without a video track.
var ErrNoVideo = errors.New("mp4: no video track")

// streamingWithin is how far into the file the movie box may start for it to be
// played as it comes, as Telegram Desktop's CheckStreamingSupport has it.
const streamingWithin = 128 << 10

const (
	// maxTopBoxes bounds the boxes walked over: a file of nothing but empty boxes
	// would otherwise be read a header at a time, to its end.
	maxTopBoxes = 1 << 20
	// maxFragmentBox bounds a fragment's box read to count what it holds.
	maxFragmentBox = 16 << 20
)

// Meta is what an MP4, MOV, M4V or 3GP file says of its video, of any codec.
type Meta struct {
	// Width and Height are the size it shows at: with its pixels' shape and its
	// turn.
	Width, Height int
	// Turn is how many degrees clockwise it is turned to show: 0, 90, 180 or 270.
	Turn int
	// Duration is how long the video lasts.
	Duration time.Duration
	// Codec is the name of the video's sample entry: avc1, hvc1, av01…
	Codec string
	// Sound tells whether a sound track goes with it.
	Sound bool
	// Streaming tells whether it can be played as it comes: its movie box comes
	// before its data, at the start.
	Streaming bool
}

// ReadMeta reads what the file of size bytes that r reads says of its video. Of
// the file it reads the box headers and the movie box, wherever it is; a file in
// fragments that does not say how long it lasts has the fragments' boxes read too.
func ReadMeta(r io.ReaderAt, size int64) (*Meta, error) {
	var moov []byte
	moovEnd, moovAt := int64(0), int64(0)
	dataFirst, sawFtyp := false, false
	for at, n := int64(0), 0; at < size && moov == nil; n++ {
		if n == maxTopBoxes {
			return nil, ErrNotMP4
		}
		b, err := boxAt(r, at, size)
		if err != nil {
			if !sawFtyp {
				return nil, ErrNotMP4
			}
			return nil, err
		}
		if b.length < b.header || b.length > size-at {
			if !sawFtyp {
				return nil, ErrNotMP4
			}
			return nil, fmt.Errorf("mp4: %q box of %d bytes in %d", b.kind, b.length, size-at)
		}
		switch b.kind {
		case "ftyp":
			sawFtyp = true
		case "mdat":
			dataFirst = true
		case "moov":
			if moov, err = b.read(r, at); err != nil {
				return nil, err
			}
			moovAt, moovEnd = at, at+b.length
		}
		at += b.length
	}
	if moov == nil {
		if !sawFtyp {
			return nil, ErrNotMP4
		}
		return nil, fmt.Errorf("mp4: no moov box")
	}
	movie := readMovie(moov)
	t := movie.video()
	if t == nil {
		return nil, ErrNoVideo
	}
	m := &Meta{Width: t.width, Height: t.height, Turn: t.turn, Codec: t.codec, Streaming: !dataFirst && moovAt < streamingWithin}
	for _, other := range movie.tracks {
		m.Sound = m.Sound || other.audio
	}
	if m.Width <= 0 || m.Height <= 0 {
		return nil, fmt.Errorf("mp4: size %dx%d", m.Width, m.Height)
	}
	if t.turn%180 != 0 {
		m.Width, m.Height = m.Height, m.Width
	}
	m.Duration = movie.duration(t)
	if movie.fragmented && movie.fragmentDuration == 0 {
		m.Duration = movie.withFragments(r, moovEnd, size, t)
	}
	if m.Duration <= 0 {
		return nil, fmt.Errorf("mp4: no duration")
	}
	return m, nil
}

// metaMovie is what is read of a movie box.
type metaMovie struct {
	timescale uint32
	// longLength is the movie's length, of either version of its box.
	longLength uint64
	// fragmented is set for a file in fragments, fragmentDuration being its whole
	// length if it tells it, and defaults the length of a sample of each track.
	fragmented       bool
	fragmentDuration uint64
	defaults         map[uint32]uint32
	tracks           []*metaTrack
}

// metaTrack is what is read of a track.
type metaTrack struct {
	id               uint32
	enabled          bool
	video, audio     bool
	codec            string
	width, height    int
	turn             int
	headerLength     uint64
	mediaLength      uint64
	mediaTimescale   uint32
	headerW, headerH int
}

// walkSome is walk over a box that may end in what is no box: what is before that
// is all there is to read.
func walkSome(data []byte, fn func(kind string, body []byte)) {
	for len(data) >= 8 {
		size := uint64(binary.BigEndian.Uint32(data))
		header := uint64(8)
		switch size {
		case 0:
			size = uint64(len(data))
		case 1:
			if len(data) < 16 {
				return
			}
			size, header = binary.BigEndian.Uint64(data[8:]), 16
		}
		if size < header || size > uint64(len(data)) {
			return
		}
		fn(string(data[4:8]), data[header:size])
		data = data[size:]
	}
}

// readMovie reads the movie box. What is missing is left zero: nothing here is
// needed unless the end says so.
func readMovie(moov []byte) *metaMovie {
	m := &metaMovie{defaults: map[uint32]uint32{}}
	walkSome(moov, func(kind string, body []byte) {
		switch kind {
		case "mvhd":
			m.timescale, m.longLength = readLength(body)
		case "mvex":
			m.fragmented = true
			walkSome(body, func(kind string, body []byte) {
				switch {
				case kind == "mehd" && len(body) >= 8:
					if body[0] == 1 && len(body) >= 12 {
						m.fragmentDuration = binary.BigEndian.Uint64(body[4:])
					} else {
						m.fragmentDuration = uint64(binary.BigEndian.Uint32(body[4:]))
					}
				case kind == "trex" && len(body) >= 20:
					// The track, the description's index, then the sample's length.
					m.defaults[binary.BigEndian.Uint32(body[4:])] = binary.BigEndian.Uint32(body[12:])
				}
			})
		case "trak":
			m.tracks = append(m.tracks, readTrack(body))
		}
	})
	return m
}

// readLength reads the timescale and the length of an mvhd or mdhd box, two dates
// standing before them; a length of all ones tells none.
func readLength(body []byte) (timescale uint32, length uint64) {
	version, rest, err := fullBox(body)
	if err != nil {
		return 0, 0
	}
	if version == 1 {
		if len(rest) < 28 {
			return 0, 0
		}
		timescale, length = binary.BigEndian.Uint32(rest[16:]), binary.BigEndian.Uint64(rest[20:])
		if length == math.MaxUint64 {
			length = 0
		}
		return timescale, length
	}
	if len(rest) < 16 {
		return 0, 0
	}
	timescale, length = binary.BigEndian.Uint32(rest[8:]), uint64(binary.BigEndian.Uint32(rest[12:]))
	if length == math.MaxUint32 {
		length = 0
	}
	return timescale, length
}

func readTrack(body []byte) *metaTrack {
	t := &metaTrack{}
	walkSome(body, func(kind string, body []byte) {
		switch kind {
		case "tkhd":
			t.readHeader(body)
		case "mdia":
			walkSome(body, func(kind string, body []byte) {
				switch kind {
				case "mdhd":
					t.mediaTimescale, t.mediaLength = readLength(body)
				case "hdlr":
					if len(body) >= 12 {
						t.video = string(body[8:12]) == "vide"
						t.audio = string(body[8:12]) == "soun"
					}
				case "minf":
					walkSome(body, func(kind string, body []byte) {
						if kind != "stbl" {
							return
						}
						walkSome(body, func(kind string, body []byte) {
							if kind == "stsd" && len(body) >= 8 {
								t.readEntry(body[8:])
							}
						})
					})
				}
			})
		}
	})
	// The header's size, for a track whose description has none.
	if t.width <= 0 || t.height <= 0 {
		t.width, t.height = t.headerW, t.headerH
	}
	return t
}

// readHeader reads a tkhd box: the track's id, its length in the movie's units,
// its size and the matrix that turns it.
func (t *metaTrack) readHeader(body []byte) {
	version, rest, err := fullBox(body)
	if err != nil {
		return
	}
	t.enabled = body[3]&1 != 0
	// Two dates, the id, four bytes and the length, then eight bytes, the layer,
	// the group, the volume and two bytes.
	matrix := 36
	if version == 1 {
		if len(rest) < 32 {
			return
		}
		t.id, t.headerLength = binary.BigEndian.Uint32(rest[16:]), binary.BigEndian.Uint64(rest[24:])
		matrix = 48
	} else {
		if len(rest) < 20 {
			return
		}
		t.id, t.headerLength = binary.BigEndian.Uint32(rest[8:]), uint64(binary.BigEndian.Uint32(rest[16:]))
	}
	if len(rest) < matrix+44 {
		return
	}
	a, b := int32(binary.BigEndian.Uint32(rest[matrix:])), int32(binary.BigEndian.Uint32(rest[matrix+4:]))
	switch {
	case a == 0 && b > 0:
		t.turn = 90
	case a == 0 && b < 0:
		t.turn = 270
	case a < 0 && b == 0:
		t.turn = 180
	}
	t.headerW = int(binary.BigEndian.Uint32(rest[matrix+36:]) >> 16)
	t.headerH = int(binary.BigEndian.Uint32(rest[matrix+40:]) >> 16)
}

// readEntry reads the first sample description, of any codec: a visual one tells
// its size at the same place whatever comes after, and the shape of its pixels in
// a box of its own.
func (t *metaTrack) readEntry(entries []byte) {
	done := false
	walkSome(entries, func(kind string, body []byte) {
		if done {
			return
		}
		done = true
		t.codec = kind
		if !t.video || len(body) < 78 {
			return
		}
		t.width = int(binary.BigEndian.Uint16(body[24:]))
		t.height = int(binary.BigEndian.Uint16(body[26:]))
		walkSome(body[78:], func(kind string, body []byte) {
			if kind != "pasp" || len(body) < 8 {
				return
			}
			h, v := binary.BigEndian.Uint32(body), binary.BigEndian.Uint32(body[4:])
			// A file may state any; beyond this it is no video to show as it says.
			if h == 0 || v == 0 {
				return
			}
			if aspect := float64(h) / float64(v); aspect >= .1 && aspect <= 10 {
				t.width = int(math.Round(float64(t.width) * aspect))
			}
		})
	})
}

// video is the track that is the file's video: the first one on, or the first.
func (m *metaMovie) video() *metaTrack {
	var first *metaTrack
	for _, t := range m.tracks {
		if !t.video {
			continue
		}
		if t.enabled {
			return t
		}
		if first == nil {
			first = t
		}
	}
	return first
}

// scaled is length units of timescale as a time, 0 for one nothing can mean.
func scaled(length uint64, timescale uint32) time.Duration {
	if timescale == 0 || length == 0 {
		return 0
	}
	seconds := float64(length) / float64(timescale)
	// A hundred years: past that a hostile table, not a video.
	if seconds > 100*365*24*3600 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// duration is how long t lasts as the movie box tells it.
func (m *metaMovie) duration(t *metaTrack) time.Duration {
	if m.fragmented && m.fragmentDuration != 0 {
		return scaled(m.fragmentDuration, m.timescale)
	}
	if d := scaled(t.mediaLength, t.mediaTimescale); d > 0 {
		return d
	}
	if d := scaled(t.headerLength, m.timescale); d > 0 {
		return d
	}
	return scaled(m.longLength, m.timescale)
}

// withFragments is how long t lasts, the file being in fragments that do not say:
// what the movie box holds and what each fragment after it, from at, adds. It
// is 0 when that is more than can be read.
func (m *metaMovie) withFragments(r io.ReaderAt, at, size int64, t *metaTrack) time.Duration {
	if t.mediaTimescale == 0 {
		return 0
	}
	total := t.mediaLength
	for n := 0; at < size; n++ {
		if n == maxTopBoxes {
			return 0
		}
		b, err := boxAt(r, at, size)
		if err != nil || b.length < b.header || b.length > size-at {
			// The rest of a file cut short: what came before is what there is.
			break
		}
		if b.kind == "moof" {
			if b.length-b.header > maxFragmentBox {
				return 0
			}
			body := make([]byte, b.length-b.header)
			if _, err := r.ReadAt(body, at+b.header); err != nil && !errors.Is(err, io.EOF) {
				return 0
			}
			total += fragmentLength(body, t.id, m.defaults[t.id])
		}
		at += b.length
	}
	return scaled(total, t.mediaTimescale)
}

// fragmentLength adds up the samples of track in a moof box, in the track's units.
func fragmentLength(moof []byte, track, fallback uint32) uint64 {
	var total uint64
	walkSome(moof, func(kind string, body []byte) {
		if kind != "traf" {
			return
		}
		own, sample := false, fallback
		walkSome(body, func(kind string, body []byte) {
			switch kind {
			case "tfhd":
				if id, length, ok := readFragmentHeader(body); ok && id == track {
					own = true
					if length != 0 {
						sample = length
					}
				}
			case "trun":
				if own {
					total += runLength(body, sample)
				}
			}
		})
	})
	return total
}

// readFragmentHeader reads a tfhd box: the track and, if it tells it, a sample's
// length.
func readFragmentHeader(body []byte) (track, length uint32, ok bool) {
	if len(body) < 8 {
		return 0, 0, false
	}
	flags := binary.BigEndian.Uint32(body) & 0xffffff
	track = binary.BigEndian.Uint32(body[4:])
	at := 8
	if flags&0x1 != 0 {
		at += 8 // The base offset.
	}
	if flags&0x2 != 0 {
		at += 4 // The description's index.
	}
	if flags&0x8 != 0 && len(body) >= at+4 {
		length = binary.BigEndian.Uint32(body[at:])
	}
	return track, length, true
}

// runLength adds up the samples of a trun box, each sample by default.
func runLength(body []byte, sample uint32) uint64 {
	if len(body) < 8 {
		return 0
	}
	flags := binary.BigEndian.Uint32(body) & 0xffffff
	count := binary.BigEndian.Uint32(body[4:])
	if flags&0x100 == 0 {
		return uint64(count) * uint64(sample)
	}
	at := 8
	if flags&0x1 != 0 {
		at += 4 // The data's offset.
	}
	if flags&0x4 != 0 {
		at += 4 // The first sample's flags.
	}
	// Each sample's own fields, of which its length comes first.
	stride := 4
	for _, field := range []uint32{0x200, 0x400, 0x800} {
		if flags&field != 0 {
			stride += 4
		}
	}
	if at > len(body) || uint64(count)*uint64(stride) > uint64(len(body)-at) {
		return 0
	}
	var total uint64
	for i := range int(count) {
		total += uint64(binary.BigEndian.Uint32(body[at+i*stride:]))
	}
	return total
}
