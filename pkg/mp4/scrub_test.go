// SPDX-License-Identifier: Unlicense OR MIT

package mp4

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"testing"
	"testing/iotest"
)

func mkbox(kind string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(out, kind...), body...)
}

// largeBox is a box with a 64-bit size.
func largeBox(kind string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := binary.BigEndian.AppendUint32(nil, 1)
	out = append(out, kind...)
	out = binary.BigEndian.AppendUint64(out, uint64(16+len(body)))
	return append(out, body...)
}

// header is a movie, track or media header of version with its times.
func header(kind string, version byte, created, modified uint64) []byte {
	body := []byte{version, 0, 0, 0}
	if version == 1 {
		body = binary.BigEndian.AppendUint64(body, created)
		body = binary.BigEndian.AppendUint64(body, modified)
	} else {
		body = binary.BigEndian.AppendUint32(body, uint32(created))
		body = binary.BigEndian.AppendUint32(body, uint32(modified))
	}
	return mkbox(kind, body, make([]byte, 20))
}

// taggedMovie is an MP4 with what a phone tells of it: the location, the
// make, XMP, dates, before and after its media.
func taggedMovie() []byte {
	return bytes.Join([][]byte{
		mkbox("ftyp", []byte("isom\x00\x00\x02\x00isomiso2mp41")),
		mkbox("moov",
			header("mvhd", 0, 3700000000, 3700000001),
			mkbox("trak",
				header("tkhd", 1, 3700000002, 3700000003),
				mkbox("mdia", header("mdhd", 0, 3700000004, 3700000005), mkbox("hdlr", make([]byte, 24))),
				mkbox("udta", mkbox("\xa9nam", []byte("my holiday"))),
			),
			mkbox("udta", mkbox("\xa9xyz", []byte("+55.7558+037.6173/"))),
			mkbox("meta", []byte{0, 0, 0, 0}, mkbox("keys", []byte("com.apple.quicktime.location.ISO6709")), mkbox("ilst", []byte("Apple iPhone 15 Pro"))),
		),
		mkbox("uuid", xmpUUID, []byte("<x:xmpmeta GPSLatitude=55.7>")),
		largeBox("udta", []byte("Xtra camera serial")),
		mkbox("mdat", []byte("THE MEDIA ITSELF")),
	}, nil)
}

// What tells where, when and with what a video was made goes: its boxes become
// free ones of zeros, its times zero. The file keeps its size, its media and
// every offset, whatever size the reads are.
func TestMetadataPatches(t *testing.T) {
	data := taggedMovie()
	patches, err := MetadataPatches(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	read := func(r io.Reader) []byte {
		out, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := read(Patched(bytes.NewReader(data), int64(len(data)), patches))
	if len(out) != len(data) {
		t.Fatalf("%d bytes, were %d", len(out), len(data))
	}
	for _, gone := range []string{"55.7558", "holiday", "quicktime.location", "iPhone", "xmpmeta", "serial", "udta", "meta"} {
		if bytes.Contains(out, []byte(gone)) {
			t.Errorf("%q is still there", gone)
		}
	}
	for _, kept := range []string{"ftyp", "isom", "moov", "mvhd", "trak", "mdia", "hdlr", "mdat", "THE MEDIA ITSELF", "free"} {
		if !bytes.Contains(out, []byte(kept)) {
			t.Errorf("%q is gone", kept)
		}
	}
	for _, kind := range []string{"mvhd", "tkhd", "mdhd"} {
		i := bytes.Index(out, []byte(kind))
		if i < 0 || !bytes.Equal(out[i+8:i+12], make([]byte, 4)) {
			t.Errorf("the times of %s stay", kind)
		}
	}
	// The large box's 64-bit size stays, and the walk over the patched file
	// goes through it.
	if again, err := MetadataPatches(bytes.NewReader(out), int64(len(out))); err != nil {
		t.Fatalf("the patched file does not read through: %v", err)
	} else if len(again) != 3 {
		// Only the times are patched again; the free boxes are none of its.
		t.Fatalf("%d patches of the patched file", len(again))
	}
	if one := read(iotest.OneByteReader(Patched(bytes.NewReader(data), int64(len(data)), patches))); !bytes.Equal(one, out) {
		t.Fatal("read a byte at a time, it differs")
	}
}

// A file with nothing to scrub has no patches; a real one still demuxes the same.
func TestMetadataPatchesKeepMedia(t *testing.T) {
	plain := bytes.Join([][]byte{mkbox("ftyp", []byte("isom")), mkbox("mdat", []byte("x"))}, nil)
	if p, err := MetadataPatches(bytes.NewReader(plain), int64(len(plain))); err != nil || len(p) != 0 {
		t.Fatalf("%v %v", p, err)
	}
	data, err := os.ReadFile("testdata/tone.m4a")
	if err != nil {
		t.Fatal(err)
	}
	patches, err := MetadataPatches(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(Patched(bytes.NewReader(data), int64(len(data)), patches))
	if err != nil {
		t.Fatal(err)
	}
	before, err := DemuxAudio(data)
	if err != nil {
		t.Skip("no audio demuxer for the test file:", err)
	}
	after, err := DemuxAudio(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Units) != len(after.Units) || before.Duration() != after.Duration() || !bytes.Equal(before.Config, after.Config) {
		t.Fatalf("%d units of %v, were %d of %v", len(after.Units), after.Duration(), len(before.Units), before.Duration())
	}
	for i := range before.Units {
		a, _ := before.Unit(i, nil)
		b, _ := after.Unit(i, nil)
		if !bytes.Equal(a, b) {
			t.Fatalf("unit %d differs", i)
		}
	}
}

// The demo's video, scrubbed, demuxes into the same pictures.
func TestMetadataPatchesKeepVideo(t *testing.T) {
	data, err := os.ReadFile("../../assets/video.mp4")
	if err != nil {
		t.Skip(err)
	}
	patches, err := MetadataPatches(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(Patched(bytes.NewReader(data), int64(len(data)), patches))
	if err != nil {
		t.Fatal(err)
	}
	before, err := Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Demux(out)
	if err != nil {
		t.Fatal(err)
	}
	if before.Len() != after.Len() || before.Width != after.Width || before.Duration != after.Duration {
		t.Fatalf("%d pictures, were %d", after.Len(), before.Len())
	}
	for i := range before.Len() {
		a, _ := before.Sample(i, nil)
		b, _ := after.Sample(i, nil)
		if !bytes.Equal(a, b) {
			t.Fatalf("picture %d differs", i)
		}
	}
	t.Logf("%d patches", len(patches))
}
