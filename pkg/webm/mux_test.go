// SPDX-License-Identifier: Unlicense OR MIT

package webm

import (
	"bytes"
	"testing"
	"time"
)

// What Mux writes Demux reads back: the frames, their alpha, their times,
// the size of the track and its frame duration, across clusters, with
// payloads whose sizes take EBML sizes of one to three bytes.
func TestMuxRoundTrip(t *testing.T) {
	const fd = time.Second / 30
	var frames []MuxFrame
	for i := range 40 {
		f := MuxFrame{Data: bytes.Repeat([]byte{byte(i)}, []int{5, 126, 127, 300, 70000}[i%5]), Timestamp: time.Duration(i) * fd, Key: i%15 == 0}
		if i%2 == 0 {
			f.Alpha = bytes.Repeat([]byte{byte(200 + i)}, 16383+i)
		}
		frames = append(frames, f)
	}
	data, err := Mux(512, 256, fd, frames)
	if err != nil {
		t.Fatal(err)
	}
	file, err := Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Width != 512 || file.Height != 256 || file.FrameDuration != fd || file.CodecID != "V_VP9" || len(file.Frames) != len(frames) {
		t.Fatalf("%dx%d %v %q, %d frames", file.Width, file.Height, file.FrameDuration, file.CodecID, len(file.Frames))
	}
	for i, f := range file.Frames {
		want := frames[i]
		// Times are kept to the millisecond.
		if !bytes.Equal(f.Data, want.Data) || !bytes.Equal(f.Alpha, want.Alpha) || (f.Timestamp-want.Timestamp).Abs() >= time.Millisecond {
			t.Fatalf("frame %d: %d bytes, %d of alpha, at %v", i, len(f.Data), len(f.Alpha), f.Timestamp)
		}
	}
	if _, err := Mux(8, 8, fd, frames[1:]); err == nil {
		t.Fatal("frames that do not start with a key frame muxed")
	}
}

// EBML sizes are the shortest, never the all-ones value of their length,
// which means "unknown".
func TestAppendSize(t *testing.T) {
	for n, want := range map[uint64][]byte{
		0: {0x80}, 126: {0xFE}, 127: {0x40, 0x7F}, 16382: {0x7F, 0xFE}, 16383: {0x20, 0x3F, 0xFF},
	} {
		if got := appendSize(nil, n); !bytes.Equal(got, want) {
			t.Errorf("%d: % x, want % x", n, got, want)
		}
	}
}
