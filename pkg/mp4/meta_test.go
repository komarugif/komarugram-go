// SPDX-License-Identifier: Unlicense OR MIT

package mp4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"time"
)

func metaBox(kind string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(len(body)+8))
	out = append(out, kind...)
	return append(out, body...)
}

func be32(values ...uint32) []byte {
	var out []byte
	for _, v := range values {
		out = binary.BigEndian.AppendUint32(out, v)
	}
	return out
}

// metaFile describes a file to build.
type metaFile struct {
	codec         string
	width, height uint16
	// a and b are the top of the track's matrix.
	a, b       int32
	timescale  uint32
	length     uint32
	sound      bool
	pasp       [2]uint32
	fragmented bool
	mehd       uint32
}

func (f metaFile) moov() []byte {
	mvhd := metaBox("mvhd", be32(0, 0, 0, 1000, 0), make([]byte, 80))
	matrix := be32(uint32(f.a), uint32(f.b))
	tkhd := metaBox("tkhd", be32(3, 0, 0, 7, 0, 0), make([]byte, 16), matrix, make([]byte, 28), be32(uint32(f.width)<<16, uint32(f.height)<<16))
	entry := make([]byte, 78)
	binary.BigEndian.PutUint16(entry[24:], f.width)
	binary.BigEndian.PutUint16(entry[26:], f.height)
	if f.pasp != [2]uint32{} {
		entry = append(entry, metaBox("pasp", be32(f.pasp[0], f.pasp[1]))...)
	}
	stsd := metaBox("stsd", be32(0, 1), metaBox(f.codec, entry))
	video := metaBox("trak", tkhd, metaBox("mdia",
		metaBox("mdhd", be32(0, 0, 0, f.timescale, f.length), make([]byte, 4)),
		metaBox("hdlr", be32(0, 0), []byte("vide"), make([]byte, 12)),
		metaBox("minf", metaBox("stbl", stsd)),
	))
	parts := [][]byte{mvhd, video}
	if f.sound {
		parts = append(parts, metaBox("trak", metaBox("mdia", metaBox("hdlr", be32(0, 0), []byte("soun"), make([]byte, 12)))))
	}
	if f.fragmented {
		mvex := [][]byte{metaBox("trex", be32(0, 7, 1, 1000, 0, 0))}
		if f.mehd != 0 {
			mvex = append(mvex, metaBox("mehd", be32(0, f.mehd)))
		}
		parts = append(parts, metaBox("mvex", mvex...))
	}
	return metaBox("moov", parts...)
}

func readMeta(t *testing.T, data []byte) *Meta {
	t.Helper()
	m, err := ReadMeta(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

var ftyp = metaBox("ftyp", []byte("isom"), be32(0))

// Any codec is told, HEVC as an iPhone records it: the demuxer of the players
// refuses it.
func TestReadMetaAnyCodec(t *testing.T) {
	data := append(append([]byte{}, ftyp...), metaFile{codec: "hvc1", width: 1920, height: 1080, a: 1 << 16, timescale: 600, length: 1500, sound: true}.moov()...)
	data = append(data, metaBox("mdat", make([]byte, 100))...)
	m := readMeta(t, data)
	if m.Width != 1920 || m.Height != 1080 || m.Duration != 2500*time.Millisecond || m.Codec != "hvc1" || !m.Sound || m.Turn != 0 {
		t.Fatalf("%+v", m)
	}
	if _, err := DemuxAt(bytes.NewReader(data), int64(len(data))); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("the players' demuxer: %v", err)
	}
}

// A quarter turn swaps the sides, and says which it was.
func TestReadMetaTurn(t *testing.T) {
	for _, c := range []struct {
		a, b          int32
		turn          int
		width, height int
	}{
		{0, 1 << 16, 90, 1080, 1920},
		{0, -1 << 16, 270, 1080, 1920},
		{-1 << 16, 0, 180, 1920, 1080},
		{1 << 16, 0, 0, 1920, 1080},
	} {
		data := append(append([]byte{}, ftyp...), metaFile{codec: "avc1", width: 1920, height: 1080, a: c.a, b: c.b, timescale: 600, length: 600}.moov()...)
		m := readMeta(t, data)
		if m.Turn != c.turn || m.Width != c.width || m.Height != c.height {
			t.Fatalf("matrix %d %d: %+v", c.a, c.b, m)
		}
	}
}

// Pixels of another shape show wider, as ffprobe's own aspect makes them.
func TestReadMetaPixelShape(t *testing.T) {
	data := append(append([]byte{}, ftyp...), metaFile{codec: "avc1", width: 720, height: 576, a: 1 << 16, timescale: 600, length: 600, pasp: [2]uint32{16, 15}}.moov()...)
	if m := readMeta(t, data); m.Width != 768 || m.Height != 576 {
		t.Fatalf("%+v", m)
	}
}

// A video plays as it comes only with its movie box before its data, at the start:
// only then has a player what it needs from the first bytes, Telegram's request
// included, and any MP4 used to say it does.
func TestReadMetaStreaming(t *testing.T) {
	moov := metaFile{codec: "avc1", width: 16, height: 16, a: 1 << 16, timescale: 600, length: 600}.moov()
	mdat := metaBox("mdat", make([]byte, 100))
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	if m := readMeta(t, join(ftyp, moov, mdat)); !m.Streaming {
		t.Fatal("movie box first: not streaming")
	}
	if m := readMeta(t, join(ftyp, mdat, moov)); m.Streaming {
		t.Fatal("movie box last: streaming")
	}
	free := metaBox("free", make([]byte, streamingWithin))
	if m := readMeta(t, join(ftyp, free, moov, mdat)); m.Streaming {
		t.Fatal("movie box far from the start: streaming")
	}
}

// Of a large file it reads headers and the movie box, wherever that is.
func TestReadMetaReadsLittle(t *testing.T) {
	moov := metaFile{codec: "avc1", width: 16, height: 16, a: 1 << 16, timescale: 600, length: 600}.moov()
	mdat := metaBox("mdat", make([]byte, 1<<24))
	data := bytes.Join([][]byte{ftyp, mdat, moov}, nil)
	c := &counting{data: data}
	if _, err := ReadMeta(c, int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if c.read > len(moov)+100 {
		t.Fatalf("read %d bytes of %d, the movie box being %d", c.read, len(data), len(moov))
	}
}

// A file in fragments tells its whole length, or has it added up.
func TestReadMetaFragments(t *testing.T) {
	told := bytes.Join([][]byte{ftyp, metaFile{codec: "avc1", width: 16, height: 16, a: 1 << 16, timescale: 1000, fragmented: true, mehd: 2000}.moov()}, nil)
	if m := readMeta(t, told); m.Duration != 2*time.Second {
		t.Fatalf("told: %+v", m)
	}
	// Each sample's length told; of the fragment's default; of the track's default;
	// and another track's, which is not counted.
	own := metaBox("moof", metaBox("traf", metaBox("tfhd", be32(0, 7)), metaBox("trun", be32(0x100|0x200, 2, 300, 1, 400, 1))))
	byFragment := metaBox("moof", metaBox("traf", metaBox("tfhd", be32(0x8, 7, 50)), metaBox("trun", be32(0, 10))))
	byTrack := metaBox("moof", metaBox("traf", metaBox("tfhd", be32(0, 7)), metaBox("trun", be32(1, 1, 0))))
	other := metaBox("moof", metaBox("traf", metaBox("tfhd", be32(0x8, 9, 5000)), metaBox("trun", be32(0, 9))))
	data := bytes.Join([][]byte{ftyp, metaFile{codec: "avc1", width: 16, height: 16, a: 1 << 16, timescale: 1000, fragmented: true}.moov(), own, metaBox("mdat", make([]byte, 8)), byFragment, byTrack, other}, nil)
	// 300 + 400 + 10×50 + 1000
	if m := readMeta(t, data); m.Duration != 2200*time.Millisecond {
		t.Fatalf("added up: %+v", m)
	}
}

// What is not a video, or is cut short anywhere, is an error and no panic.
func TestReadMetaBroken(t *testing.T) {
	for _, junk := range [][]byte{[]byte("not a video at all, not even close"), {0x1A, 0x45, 0xDF, 0xA3, 0, 0, 0, 0}, {}} {
		if _, err := ReadMeta(bytes.NewReader(junk), int64(len(junk))); !errors.Is(err, ErrNotMP4) {
			t.Fatalf("%q: %v", junk, err)
		}
	}
	sound, err := os.ReadFile("testdata/tone.m4a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMeta(bytes.NewReader(sound), int64(len(sound))); !errors.Is(err, ErrNoVideo) {
		t.Fatalf("sound alone: %v", err)
	}
	whole := bytes.Join([][]byte{ftyp, metaFile{codec: "avc1", width: 16, height: 16, a: 1 << 16, timescale: 1000, sound: true, pasp: [2]uint32{1, 1}, fragmented: true}.moov(), metaBox("moof", metaBox("traf", metaBox("tfhd", be32(0x8, 7, 50)), metaBox("trun", be32(0x100, 1, 50))))}, nil)
	for n := range whole {
		ReadMeta(bytes.NewReader(whole[:n]), int64(n))
	}
	for at := range whole {
		broken := bytes.Clone(whole)
		broken[at] ^= 0xff
		ReadMeta(bytes.NewReader(broken), int64(len(broken)))
	}
}

// The video in the repository is told as the players' demuxer reads it.
func TestReadMetaIsDemux(t *testing.T) {
	data, err := os.ReadFile("../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	whole, err := DemuxAt(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	m := readMeta(t, data)
	if m.Width != whole.Width || m.Height != whole.Height || m.Codec != "avc1" {
		t.Fatalf("%+v, not %dx%d", m, whole.Width, whole.Height)
	}
	if d := m.Duration - whole.Duration; d < -100*time.Millisecond || d > 100*time.Millisecond {
		t.Fatalf("%v, not %v", m.Duration, whole.Duration)
	}
}
