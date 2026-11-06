// SPDX-License-Identifier: Unlicense OR MIT

package webm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"
)

// el is an element of a known size.
func el(id uint32, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := metaID(id)
	out = binary.BigEndian.AppendUint64(out, uint64(len(body))|1<<56)
	return append(out, body...)
}

// open is the start of an element of unknown size.
func open(id uint32) []byte {
	return append(metaID(id), 0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
}

func metaID(id uint32) []byte {
	out := binary.BigEndian.AppendUint32(nil, id)
	for out[0] == 0 {
		out = out[1:]
	}
	return out
}

func uintEl(id uint32, v uint64) []byte {
	return el(id, binary.BigEndian.AppendUint64(nil, v))
}

func floatEl(id uint32, v float64) []byte {
	return el(id, binary.BigEndian.AppendUint64(nil, math.Float64bits(v)))
}

func simpleBlock(track byte, relative int16) []byte {
	body := append([]byte{0x80 | track}, byte(uint16(relative)>>8), byte(relative), 0x80, 1, 2, 3)
	return el(idSimpleBlock, body)
}

var ebmlHeader = el(0x1A45DFA3, el(0x4282, []byte("webm")))

func videoEntry(number uint64, codec string, extra ...[]byte) []byte {
	return el(idTrackEntry, uintEl(idTrackNumber, number), uintEl(idTrackType, trackTypeVideo), el(idCodecID, []byte(codec)),
		el(idVideo, append([][]byte{uintEl(idPixelWidth, 640), uintEl(idPixelHeight, 360)}, extra...)...))
}

func audioEntry(number uint64) []byte {
	return el(idTrackEntry, uintEl(idTrackNumber, number), uintEl(idTrackType, trackTypeAudio), el(idCodecID, []byte("A_OPUS")))
}

func readMeta(t *testing.T, data []byte) *Meta {
	t.Helper()
	m, err := ReadMeta(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// counting counts what is read.
type counting struct {
	r    *bytes.Reader
	read int
}

func (c *counting) ReadAt(p []byte, at int64) (int, error) {
	n, err := c.r.ReadAt(p, at)
	c.read += n
	return n, err
}

// A file that says its length, whatever codec, is read up to its clusters, which
// Demux reads whole.
func TestReadMetaKnownLength(t *testing.T) {
	cluster := el(idCluster, uintEl(idTimecode, 0), simpleBlock(1, 0), el(0xEC, make([]byte, 1<<22)))
	data := bytes.Join([][]byte{ebmlHeader, el(idSegment,
		el(idInfo, floatEl(idDuration, 2500), uintEl(idTimecodeScale, 1000000)),
		el(idTracks, videoEntry(1, "V_MPEGH/ISO/HEVC"), audioEntry(2)),
		cluster,
	)}, nil)
	m := readMeta(t, data)
	if m.Width != 640 || m.Height != 360 || m.Duration != 2500*time.Millisecond || m.CodecID != "V_MPEGH/ISO/HEVC" || !m.Sound {
		t.Fatalf("%+v", m)
	}
	c := &counting{r: bytes.NewReader(data)}
	if _, err := ReadMeta(c, int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if c.read > len(data)/4 {
		t.Fatalf("read %d bytes of %d", c.read, len(data))
	}
}

// The scale may come after the length it scales.
func TestReadMetaScaleAfterLength(t *testing.T) {
	data := bytes.Join([][]byte{ebmlHeader, el(idSegment,
		el(idInfo, floatEl(idDuration, 3), uintEl(idTimecodeScale, 1000000000)),
		el(idTracks, videoEntry(1, "V_VP9")),
	)}, nil)
	if m := readMeta(t, data); m.Duration != 3*time.Second {
		t.Fatalf("%+v", m)
	}
}

// A browser's recording does not say its length, and neither its segment nor its
// clusters their sizes: it ends where the last block of the video does.
func TestReadMetaRecordingWithoutLength(t *testing.T) {
	data := bytes.Join([][]byte{ebmlHeader, open(idSegment),
		el(idInfo, uintEl(idTimecodeScale, 1000000)),
		el(idTracks, videoEntry(1, "V_VP8"), audioEntry(2)),
		open(idCluster), uintEl(idTimecode, 0), simpleBlock(1, 0), simpleBlock(1, 500),
		open(idCluster), uintEl(idTimecode, 1000), simpleBlock(1, 0),
		el(idBlockGroup, el(idBlock, simpleBlockBody(1, 200)), uintEl(idBlockDuration, 40)),
		// Sound goes on after the picture, and does not count.
		simpleBlock(2, 900),
	}, nil)
	if m := readMeta(t, data); m.Duration != 1240*time.Millisecond || !m.Sound {
		t.Fatalf("%+v", m)
	}
}

func simpleBlockBody(track byte, relative int16) []byte {
	return []byte{0x80 | track, byte(uint16(relative) >> 8), byte(relative), 0, 1}
}

// Display sizes make the shape it shows at.
func TestReadMetaDisplaySize(t *testing.T) {
	data := bytes.Join([][]byte{ebmlHeader, el(idSegment,
		el(idInfo, floatEl(idDuration, 1000)),
		el(idTracks, videoEntry(1, "V_VP9", uintEl(idDisplayWidth, 16), uintEl(idDisplayHeight, 9*2))),
	)}, nil)
	// 640×360 is 16:9; shown 16:18, it is half as wide.
	if m := readMeta(t, data); m.Width != 320 || m.Height != 360 {
		t.Fatalf("%+v", m)
	}
}

// What is not a video, or is cut short or broken anywhere, is an error or a
// wrong answer, never a panic or a read outside the file.
func TestReadMetaBroken(t *testing.T) {
	for _, junk := range [][]byte{[]byte("not a video at all, not even close"), {}, {0x1A}} {
		if _, err := ReadMeta(bytes.NewReader(junk), int64(len(junk))); !errors.Is(err, ErrNotWebM) {
			t.Fatalf("%q: %v", junk, err)
		}
	}
	noVideo := bytes.Join([][]byte{ebmlHeader, el(idSegment, el(idInfo, floatEl(idDuration, 1000)), el(idTracks, audioEntry(1)))}, nil)
	if _, err := ReadMeta(bytes.NewReader(noVideo), int64(len(noVideo))); err == nil {
		t.Fatal("sound alone read")
	}
	noLength := bytes.Join([][]byte{ebmlHeader, el(idSegment, el(idTracks, videoEntry(1, "V_VP9")))}, nil)
	if _, err := ReadMeta(bytes.NewReader(noLength), int64(len(noLength))); err == nil {
		t.Fatal("no length, no blocks: read")
	}
	whole := bytes.Join([][]byte{ebmlHeader, open(idSegment),
		el(idInfo, uintEl(idTimecodeScale, 1000000)),
		el(idTracks, videoEntry(1, "V_VP8", uintEl(idDisplayWidth, 4), uintEl(idDisplayHeight, 3))),
		open(idCluster), uintEl(idTimecode, 0), simpleBlock(1, 5),
		el(idBlockGroup, el(idBlock, simpleBlockBody(1, 9))),
	}, nil)
	for n := range whole {
		ReadMeta(bytes.NewReader(whole[:n]), int64(n))
	}
	for at := range whole {
		broken := bytes.Clone(whole)
		broken[at] ^= 0xff
		ReadMeta(bytes.NewReader(broken), int64(len(broken)))
	}
}
