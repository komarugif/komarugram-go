// SPDX-License-Identifier: Unlicense OR MIT

package unpack

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func gzipped(parts ...[]byte) []byte {
	var b bytes.Buffer
	for _, p := range parts {
		w := gzip.NewWriter(&b)
		w.Write(p)
		w.Close()
	}
	return b.Bytes()
}

func sample(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i * 7 % 251)
	}
	return data
}

func TestGzip(t *testing.T) {
	data := sample(300_000)
	for name, c := range map[string]struct {
		src   []byte
		limit int
		want  []byte
		err   error
	}{
		"whole":          {gzipped(data), 1 << 20, data, nil},
		"empty":          {gzipped(nil), 1 << 20, nil, nil},
		"two members":    {gzipped(data[:1000], data[1000:]), 1 << 20, data, nil},
		"over the limit": {gzipped(data), 1000, nil, ErrTooLarge},
		"at the limit":   {gzipped(data), len(data), data, nil},
		"trailer lies":   {lyingTrailer(gzipped(data), 10), 1 << 20, nil, gzip.ErrChecksum},
	} {
		got, err := Gzip(c.src, c.limit)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("%s: %v, want %v", name, err, c.err)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, c.want) {
			t.Errorf("%s: %d bytes, %v", name, len(got), err)
		}
	}
}

func lyingTrailer(src []byte, size uint32) []byte {
	src = bytes.Clone(src)
	binary.LittleEndian.PutUint32(src[len(src)-4:], size)
	return src
}

// The buffer is the data's size: io.ReadAll took about two and a half
// times it.
func TestGzipAllocatesItsSize(t *testing.T) {
	data := sample(2 << 20)
	src := gzipped(data)
	Gzip(src, 4<<20) // The reader's tables, once.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := Gzip(src, 4<<20)
	runtime.ReadMemStats(&after)
	if err != nil || len(got) != len(data) {
		t.Fatal(len(got), err)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > uint64(len(data))*5/4 {
		t.Errorf("allocated %d bytes for %d", alloc, len(data))
	}
}

func TestZstd(t *testing.T) {
	data := sample(1 << 20)
	e, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	got, err := Zstd(e.EncodeAll(data, nil))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal(len(got), err)
	}
	if _, err := Zstd(data[:100]); err == nil {
		t.Error("no error for data that is not zstd")
	}
}

// Every file the program embeds zstd-compressed tells its size, as zstd
// writes it when it reads a file, not a pipe: unpacking it takes one
// buffer of that size, and no window of zstd's level 22, 128 MiB, which
// the decoder refuses.
func TestEmbeddedFramesTellTheirSize(t *testing.T) {
	files, err := filepath.Glob("../*/*.zst")
	if err != nil {
		t.Fatal(err)
	}
	more, _ := filepath.Glob("../*/*/*.zst")
	files = append(files, more...)
	if len(files) < 7 {
		t.Fatalf("found %d embedded files", len(files))
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var h zstd.Header
		if err := h.Decode(data); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !h.HasFCS || h.WindowSize > h.FrameContentSize {
			t.Errorf("%s: size told %v, window %d for %d bytes: compress the file, not a pipe", name, h.HasFCS, h.WindowSize, h.FrameContentSize)
		}
	}
}
