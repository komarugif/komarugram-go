// SPDX-License-Identifier: Unlicense OR MIT

package webm

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"time"
)

// Matroska ids only what is said of a file needs.
const (
	idDisplayWidth  = 0x54B0
	idDisplayHeight = 0x54BA
	idBlockDuration = 0x9B
	idCodecPrivate  = 0x63A2
	idReference     = 0xFB

	trackTypeAudio = 2
)

const (
	// metaBoxLimit bounds the Info and Tracks read into memory.
	metaBoxLimit = 16 << 20
	// maxMetaElements bounds the elements walked over, a file of nothing but small
	// ones being read otherwise a header at a time, to its end. Hours of video
	// have fewer.
	maxMetaElements = 1 << 24
	// metaWindow is how much of a file is read at once.
	metaWindow = 64 << 10
)

// Meta is what a WebM or Matroska file says of its video, of any codec.
type Meta struct {
	// Width and Height are the size it shows at, with the shape it asks for.
	Width, Height int
	// Duration is how long it lasts.
	Duration time.Duration
	// CodecID is the video track's: V_VP9, V_MPEGH/ISO/HEVC…
	CodecID string
	// Sound tells whether a sound track goes with it.
	Sound bool
}

// ReadMeta reads what the file of size bytes that r reads says of its video. Of
// the file it reads the headers before its clusters, and when it does not say
// how long it lasts, as a browser's recording does not, the headers of its blocks.
func ReadMeta(r io.ReaderAt, size int64) (*Meta, error) {
	w := &metaReader{r: r, size: size}
	head := w.bytes(0, 4)
	if len(head) < 4 || binary.BigEndian.Uint32(head) != 0x1A45DFA3 {
		return nil, ErrNotWebM
	}
	m := &metaScan{scale: 1000000, w: w}
	at := int64(0)
	for at < size {
		e, ok := w.element(at)
		if !ok {
			break
		}
		if e.id == idSegment {
			end := size
			if !e.unknown && e.end() < size {
				end = e.end()
			}
			m.segment(e.body, end)
			break
		}
		if e.unknown {
			break
		}
		at = e.end()
	}
	return m.result()
}

// metaScan is what was read so far.
type metaScan struct {
	// index, when set, is filled with what the blocks hold.
	index *Index
	// audioTrack is the first sound track, of audioCodec, and audioHead what its
	// decoder is told.
	audioTrack uint64
	audioCodec string
	audioHead  []byte

	w     *metaReader
	scale uint64
	// length is the Info's, in nanoseconds; last the end of what was seen of the
	// blocks, in the file's ticks.
	length     float64
	last       int64
	frame      time.Duration
	tracks     bool
	videoTrack uint64
	width      int
	height     int
	displayW   uint64
	displayH   uint64
	codec      string
	sound      bool
	walked     int
}

func (m *metaScan) result() (*Meta, error) {
	if !m.tracks || m.videoTrack == 0 {
		return nil, fmt.Errorf("webm: no video track")
	}
	if m.width <= 0 || m.height <= 0 {
		return nil, fmt.Errorf("webm: size %dx%d", m.width, m.height)
	}
	out := &Meta{Width: m.width, Height: m.height, CodecID: m.codec, Sound: m.sound}
	if m.displayW > 0 && m.displayH > 0 {
		shape := float64(m.displayW) / float64(m.displayH) * float64(m.height) / float64(m.width)
		// A file may state any; beyond this it is no video to show as it says.
		if shape >= .1 && shape <= 10 {
			out.Width = int(math.Round(float64(m.width) * shape))
		}
	}
	out.Duration = time.Duration(m.length)
	if out.Duration <= 0 && m.last > 0 {
		out.Duration = time.Duration(float64(m.last)*float64(m.scale)) + m.frame
	}
	if out.Duration <= 0 {
		return nil, fmt.Errorf("webm: no duration")
	}
	return out, nil
}

// segment walks the segment's elements between at and end, until all that is to
// be known is known.
func (m *metaScan) segment(at, end int64) {
	for at < end {
		if m.done() {
			return
		}
		e, ok := m.next(at)
		if !ok {
			return
		}
		switch e.id {
		case idInfo:
			m.info(e)
		case idTracks:
			m.readTracks(e)
		case idCluster:
			if at, ok = m.cluster(e, end); !ok {
				return
			}
			continue
		}
		if e.unknown {
			return
		}
		at = e.end()
	}
}

// done tells that all there is to know is known.
func (m *metaScan) done() bool {
	if m.index != nil {
		return m.index.firstOnly && len(m.index.Video) > 0
	}
	return m.tracks && m.length > 0
}

// next is the element at, within the walk's bound.
func (m *metaScan) next(at int64) (metaElement, bool) {
	m.walked++
	if m.walked > maxMetaElements {
		return metaElement{}, false
	}
	return m.w.element(at)
}

func (m *metaScan) info(e metaElement) {
	body := m.w.body(e)
	if body == nil {
		return
	}
	var length float64
	// A broken element leaves what came before it.
	_ = (&reader{data: body}).walk(func(_ *reader, id uint32, body []byte) error {
		switch id {
		case idTimecodeScale:
			if scale := beUint(body); scale > 0 {
				m.scale = scale
			}
		case idDuration:
			length = beFloat(body)
		}
		return nil
	})
	// After the walk: the scale may come after the length.
	if length > 0 && !math.IsInf(length, 0) {
		if ns := length * float64(m.scale); ns < float64(math.MaxInt64) {
			m.length = ns
		}
	}
}

func (m *metaScan) readTracks(e metaElement) {
	body := m.w.body(e)
	if body == nil {
		return
	}
	m.tracks = true
	_ = (&reader{data: body}).walk(func(_ *reader, id uint32, body []byte) error {
		if id == idTrackEntry {
			m.track(body)
		}
		return nil
	})
}

func (m *metaScan) track(body []byte) {
	var number, kind, displayW, displayH uint64
	var width, height int
	var codec string
	var private []byte
	var frame time.Duration
	_ = (&reader{data: body}).walk(func(_ *reader, id uint32, body []byte) error {
		switch id {
		case idTrackNumber:
			number = beUint(body)
		case idTrackType:
			kind = beUint(body)
		case idCodecID:
			codec = string(trimZero(body))
		case idDefaultDuration:
			frame = time.Duration(beUint(body))
		case idCodecPrivate:
			private = body
		case idVideo:
			return (&reader{data: body}).walk(func(_ *reader, id uint32, body []byte) error {
				switch id {
				case idPixelWidth:
					width = int(beUint(body))
				case idPixelHeight:
					height = int(beUint(body))
				case idDisplayWidth:
					displayW = beUint(body)
				case idDisplayHeight:
					displayH = beUint(body)
				}
				return nil
			})
		}
		return nil
	})
	switch {
	case kind == trackTypeAudio:
		m.sound = true
		if m.audioTrack == 0 {
			m.audioTrack, m.audioCodec, m.audioHead = number, codec, private
		}
	case kind == trackTypeVideo && m.videoTrack == 0:
		m.videoTrack, m.codec, m.width, m.height, m.frame = number, codec, width, height, frame
		m.displayW, m.displayH = displayW, displayH
	}
}

// cluster notes the end of the cluster e's blocks, and is where what follows it
// starts: a cluster of unknown size lasts until an element of the segment comes.
func (m *metaScan) cluster(e metaElement, end int64) (int64, bool) {
	limit := end
	if !e.unknown && e.end() < end {
		limit = e.end()
	}
	var start int64
	at := e.body
	for at < limit {
		if m.done() {
			return 0, false
		}
		child, ok := m.next(at)
		if !ok {
			return 0, false
		}
		if e.unknown && segmentChild(child.id) {
			return at, true
		}
		switch child.id {
		case idTimecode:
			if body := m.w.bytes(child.body, int(min(child.length, 8))); len(body) == int(min(child.length, 8)) {
				start = int64(beUint(body))
			}
		case idSimpleBlock:
			m.block(child, start, 0, false)
		case idBlockGroup:
			m.blockGroup(child, start)
		}
		if child.unknown {
			return 0, false
		}
		at = child.end()
	}
	return limit, true
}

func (m *metaScan) blockGroup(group metaElement, start int64) {
	var block metaElement
	var length int64
	referring := false
	for at := group.body; at < group.end(); {
		child, ok := m.next(at)
		if !ok || child.unknown {
			return
		}
		switch child.id {
		case idBlock:
			block = child
		case idReference:
			referring = true
		case idBlockDuration:
			if body := m.w.bytes(child.body, int(min(child.length, 8))); len(body) == int(min(child.length, 8)) {
				length = int64(beUint(body))
			}
		}
		at = child.end()
	}
	if block.id != 0 {
		m.block(block, start, length, !referring)
	}
}

// block notes where the block e ends, of the video when the track is known, and
// what an index keeps of it. key tells a block of a group that refers to no other;
// a simple block tells it by itself.
func (m *metaScan) block(e metaElement, start, length int64, key bool) {
	r := &reader{data: m.w.bytes(e.body, 12)}
	track, err := r.vint(false)
	if err != nil || r.offset+3 > len(r.data) {
		return
	}
	relative := int64(int16(binary.BigEndian.Uint16(r.data[r.offset:])))
	flags := r.data[r.offset+2]
	head := int64(r.offset + 3)
	if m.index != nil && track == m.audioTrack && m.audioTrack != 0 {
		m.index.keepAudio(m, e, head, flags)
		return
	}
	if m.videoTrack != 0 && track != m.videoTrack {
		return
	}
	m.last = max(m.last, start+relative+length)
	if m.index != nil && track == m.videoTrack {
		// A simple block's top bit tells a keyframe; a group's is reserved.
		key = key || e.id == idSimpleBlock && flags&0x80 != 0
		m.index.keepVideo(e, head, flags, start+relative, key)
	}
}

// segmentChild reports whether id is that of an element of the segment: one that
// ends a cluster of unknown size.
func segmentChild(id uint32) bool {
	switch id {
	case idCluster, idInfo, idTracks, 0x114D9B74, 0x1C53BB6B, 0x1941A469, 0x1043A770, 0x1254C367:
		// With SeekHead, Cues, Attachments, Chapters and Tags.
		return true
	}
	return false
}

// metaElement is the header of an element of a file.
type metaElement struct {
	id           uint32
	body, length int64
	unknown      bool
}

func (e metaElement) end() int64 { return e.body + e.length }

// metaReader reads a file a window at a time, as headers lie near each other.
type metaReader struct {
	r    io.ReaderAt
	size int64
	at   int64
	buf  []byte
}

// bytes is up to n bytes at, n being no more than a window; fewer at the file's end.
func (w *metaReader) bytes(at int64, n int) []byte {
	if at < 0 || at >= w.size || n > metaWindow {
		return nil
	}
	if at < w.at || at+int64(n) > w.at+int64(len(w.buf)) {
		if cap(w.buf) == 0 {
			w.buf = make([]byte, metaWindow)
		}
		w.buf = w.buf[:min(metaWindow, w.size-at)]
		read, err := w.r.ReadAt(w.buf, at)
		if err != nil && read < len(w.buf) {
			w.buf = w.buf[:read]
		}
		w.at = at
	}
	from := at - w.at
	return w.buf[from:min(from+int64(n), int64(len(w.buf)))]
}

// element reads the header of the element at. Its size may run past the end of a
// file cut short, for whoever reads it to bound.
func (w *metaReader) element(at int64) (metaElement, bool) {
	r := &reader{data: w.bytes(at, 12)}
	id, err := r.elementID()
	if err != nil {
		return metaElement{}, false
	}
	size, unknown, err := r.size()
	if err != nil || size > math.MaxInt64/2 {
		return metaElement{}, false
	}
	e := metaElement{id: id, body: at + int64(r.offset), length: int64(size), unknown: unknown}
	if e.body > w.size {
		return metaElement{}, false
	}
	return e, true
}

// body reads what e holds, nil when it is unknown, or too much to read.
func (w *metaReader) body(e metaElement) []byte {
	if e.unknown || e.length > metaBoxLimit || e.end() > w.size {
		return nil
	}
	out := make([]byte, e.length)
	if _, err := w.r.ReadAt(out, e.body); err != nil && e.length > 0 {
		return nil
	}
	return out
}

// beFloat reads a Matroska float, of 4 or 8 bytes; 0 for anything else.
func beFloat(body []byte) float64 {
	switch len(body) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(body)))
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(body))
	}
	return 0
}
