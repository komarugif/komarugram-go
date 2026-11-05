// SPDX-License-Identifier: Unlicense OR MIT

package webm

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func blockAt(track byte, relative int16, flags byte, payload ...byte) []byte {
	return append([]byte{0x80 | track, byte(uint16(relative) >> 8), byte(relative), flags}, payload...)
}

// recording is a file of a video and its sound, in clusters of known and unknown
// sizes, simple blocks and groups.
func recording(extra ...[]byte) []byte {
	return bytes.Join(append([][]byte{ebmlHeader, open(idSegment),
		el(idInfo, uintEl(idTimecodeScale, 1000000)),
		el(idTracks, videoEntry(1, "V_VP9"),
			el(idTrackEntry, uintEl(idTrackNumber, 2), uintEl(idTrackType, trackTypeAudio), el(idCodecID, []byte("A_OPUS")), el(idCodecPrivate, []byte("OpusHead")))),
		open(idCluster), uintEl(idTimecode, 0),
		el(idSimpleBlock, blockAt(1, 0, 0x80, 'k', 'e', 'y')),
		el(idSimpleBlock, blockAt(2, 0, 0x80, 0xF8, 0xAA, 0xBB)),
		el(idSimpleBlock, blockAt(1, 40, 0, 'p')),
		el(idCluster, uintEl(idTimecode, 80),
			el(idBlockGroup, el(idBlock, blockAt(1, 0, 0, 'q')), uintEl(idReference, 1)),
			el(idBlockGroup, el(idBlock, blockAt(1, 40, 0, 'K', 'E', 'Y')), uintEl(idBlockDuration, 40)),
			el(idSimpleBlock, blockAt(2, 0, 0, 0xFC, 0xCC))),
	}, extra...), nil)
}

// All the frames are found where they lie, each a keyframe or not, with their times.
func TestReadIndex(t *testing.T) {
	data := recording()
	x, err := ReadIndex(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		data string
		key  bool
		at   time.Duration
	}{
		{"key", true, 0},
		{"p", false, 40 * time.Millisecond},
		{"q", false, 80 * time.Millisecond},
		{"KEY", true, 120 * time.Millisecond},
	}
	if len(x.Video) != len(want) || len(x.Times) != len(want) {
		t.Fatalf("%d pictures, %d times", len(x.Video), len(x.Times))
	}
	for i, w := range want {
		got, err := x.Read(x.Video[i], nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != w.data || x.Video[i].Key != w.key || x.Times[i] != w.at {
			t.Errorf("picture %d: %q, key %v, at %v; not %+v", i, got, x.Video[i].Key, x.Times[i], w)
		}
	}
	if x.Duration < 160*time.Millisecond || x.Duration > 200*time.Millisecond {
		t.Errorf("%v long", x.Duration)
	}
	if x.AudioCodec != "A_OPUS" || string(x.AudioHead) != "OpusHead" || len(x.Audio) != 2 {
		t.Fatalf("sound %s %q, %d packets", x.AudioCodec, x.AudioHead, len(x.Audio))
	}
	if x.Audio[0].TOC != [2]byte{0xF8, 0xAA} || x.Audio[1].TOC != [2]byte{0xFC, 0xCC} {
		t.Errorf("TOCs %v %v", x.Audio[0].TOC, x.Audio[1].TOC)
	}
	if got, _ := x.Read(x.Audio[1].Unit, nil); !bytes.Equal(got, []byte{0xFC, 0xCC}) {
		t.Errorf("packet %v", got)
	}
}

// The first picture alone stops the walk: a large file is not read through for it.
func TestReadFirstPicture(t *testing.T) {
	data := recording(el(idCluster, uintEl(idTimecode, 200), el(0xEC, make([]byte, 1<<22))))
	c := &counting{r: bytes.NewReader(data)}
	x, err := ReadFirstPicture(c, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Video) != 1 {
		t.Fatalf("%d pictures", len(x.Video))
	}
	if got, _ := x.Read(x.Video[0], nil); string(got) != "key" {
		t.Fatalf("%q", got)
	}
	if c.read > metaWindow*2 {
		t.Fatalf("read %d bytes of %d", c.read, len(data))
	}
}

// A block cut off by the end of the file holds nothing; laced ones are left out; a
// file without pictures, or that is none, is told so; and nothing panics.
func TestReadIndexBroken(t *testing.T) {
	data := recording()
	if _, err := ReadIndex(bytes.NewReader(data[:len(data)-1]), int64(len(data)-1)); err != nil {
		t.Fatal(err)
	}
	x, err := ReadIndex(bytes.NewReader(data[:len(data)-1]), int64(len(data)-1))
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Audio) != 1 {
		t.Fatalf("%d packets, the last being cut off", len(x.Audio))
	}
	laced := bytes.Join([][]byte{ebmlHeader, el(idSegment,
		el(idInfo, floatEl(idDuration, 1000)), el(idTracks, videoEntry(1, "V_VP9")),
		el(idCluster, uintEl(idTimecode, 0), el(idSimpleBlock, blockAt(1, 0, 0x02, 1, 2, 3))),
	)}, nil)
	if _, err := ReadIndex(bytes.NewReader(laced), int64(len(laced))); err == nil {
		t.Fatal("pictures in a laced block")
	}
	junk := []byte("not a video at all")
	if _, err := ReadIndex(bytes.NewReader(junk), int64(len(junk))); !errors.Is(err, ErrNotWebM) {
		t.Fatal(err)
	}
	for n := range data {
		ReadIndex(bytes.NewReader(data[:n]), int64(n))
		ReadFirstPicture(bytes.NewReader(data[:n]), int64(n))
	}
	for at := range data {
		broken := bytes.Clone(data)
		broken[at] ^= 0xff
		if x, err := ReadIndex(bytes.NewReader(broken), int64(len(broken))); err == nil {
			for _, u := range x.Video {
				if u.Offset < 0 || u.Offset+int64(u.Size) > int64(len(broken)) {
					t.Fatalf("a frame outside the file: %+v", u)
				}
			}
		}
	}
}
