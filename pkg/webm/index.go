// SPDX-License-Identifier: Unlicense OR MIT

package webm

import (
	"errors"
	"io"
	"slices"
	"time"
)

const (
	// MaxVideoUnits bounds the pictures of a video: about 19 hours at 30 fps. Their
	// table, some 40 bytes a picture, is what a hostile file makes the parser keep.
	MaxVideoUnits = 1 << 21
	// MaxAudioUnits bounds the packets of its sound: about 23 hours at 20 ms.
	MaxAudioUnits = 1 << 22
)

// ErrTooLong is a file with more blocks than are kept.
var ErrTooLong = errors.New("webm: too many blocks")

// Unit is where a block's frame lies in the file.
type Unit struct {
	Offset int64
	Size   uint32
	// Key tells a keyframe.
	Key bool
}

// AudioUnit is where a packet of sound lies, and how it starts, which tells how
// long an Opus packet plays.
type AudioUnit struct {
	Unit
	TOC [2]byte
}

// Index is where the frames of a WebM file lie, read where they lie as they are
// fed: a file costs its table, whatever its length.
type Index struct {
	Meta
	// Video are the pictures, in the order to feed them; Times are when they show,
	// from the first, in order.
	Video []Unit
	Times []time.Duration
	// AudioCodec is the sound's codec id, "" for none, AudioHead what its decoder
	// is told, and Audio its packets in order.
	AudioCodec string
	AudioHead  []byte
	Audio      []AudioUnit
	// LengthTold says that the file told its length, Duration being a guess, of a
	// walk that stopped at the first picture, if not.
	LengthTold bool
	// firstOnly stops the walk at the first picture.
	firstOnly bool
	// ticks are the pictures' times until the scale is sure.
	ticks []int64
	file  io.ReaderAt
	size  int64
	full  bool
}

// ReadIndex reads the headers of all the blocks of the file of size bytes that r
// reads: all of its length is walked over, and its frames left where they lie.
func ReadIndex(r io.ReaderAt, size int64) (*Index, error) {
	return readIndex(r, size, false)
}

// ReadFirstPicture is ReadIndex up to the first picture, for one who wants that
// alone: the first clusters are read, and the index has no length of its own unless
// the file tells it.
func ReadFirstPicture(r io.ReaderAt, size int64) (*Index, error) {
	return readIndex(r, size, true)
}

func readIndex(r io.ReaderAt, size int64, first bool) (*Index, error) {
	index := &Index{file: r, size: size, firstOnly: first}
	w := &metaReader{r: r, size: size}
	head := w.bytes(0, 4)
	if len(head) < 4 || head[0] != 0x1A || head[1] != 0x45 || head[2] != 0xDF || head[3] != 0xA3 {
		return nil, ErrNotWebM
	}
	m := &metaScan{scale: 1000000, w: w, index: index}
	for at := int64(0); at < size; {
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
	if index.full {
		return nil, ErrTooLong
	}
	index.LengthTold = m.length > 0
	if first && m.length == 0 {
		// Not looked for: the walk stopped at the first picture.
		m.length = 1
	}
	meta, err := m.result()
	if err != nil {
		return nil, err
	}
	if len(index.Video) == 0 {
		return nil, errors.New("webm: no pictures")
	}
	index.Meta = *meta
	index.AudioCodec, index.AudioHead = m.audioCodec, m.audioHead
	sorted := slices.Clone(index.ticks)
	slices.Sort(sorted)
	index.Times = make([]time.Duration, len(sorted))
	for i, tick := range sorted {
		index.Times[i] = time.Duration(float64(tick-sorted[0]) * float64(m.scale))
	}
	index.ticks = nil
	if last := index.Times[len(index.Times)-1]; index.Duration <= last {
		index.Duration = last + max(m.frame, time.Millisecond)
	}
	return index, nil
}

// keepVideo keeps the picture of the block e, whose frame comes head bytes in.
func (x *Index) keepVideo(e metaElement, head int64, flags byte, tick int64, key bool) {
	unit, ok := x.unit(e, head, flags)
	if !ok {
		return
	}
	if len(x.Video) == MaxVideoUnits {
		x.full = true
		return
	}
	unit.Key = key
	x.Video = append(x.Video, unit)
	x.ticks = append(x.ticks, tick)
}

// keepAudio keeps the packet of sound of the block e.
func (x *Index) keepAudio(m *metaScan, e metaElement, head int64, flags byte) {
	unit, ok := x.unit(e, head, flags)
	if !ok {
		return
	}
	if len(x.Audio) == MaxAudioUnits {
		x.full = true
		return
	}
	var toc [2]byte
	copy(toc[:], m.w.bytes(unit.Offset, 2))
	x.Audio = append(x.Audio, AudioUnit{Unit: unit, TOC: toc})
}

// unit is where the frame of the block e lies. Laced blocks hold several, which
// neither VP9 nor Opus in WebM does; one cut off by the end of the file holds none.
func (x *Index) unit(e metaElement, head int64, flags byte) (Unit, bool) {
	size := e.length - head
	if flags&0x06 != 0 || size <= 0 || size > 1<<31 || e.unknown || e.end() > x.size {
		return Unit{}, false
	}
	return Unit{Offset: e.body + head, Size: uint32(size)}, true
}

// Read reads u into buf, grown as needed.
func (x *Index) Read(u Unit, buf []byte) ([]byte, error) {
	if cap(buf) < int(u.Size) {
		buf = make([]byte, u.Size)
	}
	buf = buf[:u.Size]
	if _, err := x.file.ReadAt(buf, u.Offset); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf, nil
}
