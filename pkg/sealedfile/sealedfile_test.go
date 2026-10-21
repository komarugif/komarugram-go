// SPDX-License-Identifier: Unlicense OR MIT

package sealedfile

import (
	"bytes"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func content(size int) []byte {
	b := make([]byte, size)
	rand.New(rand.NewSource(int64(size))).Read(b)
	return b
}

// writeAll writes data into f chunk by chunk, the last first: chunks come in
// any order.
func writeAll(t *testing.T, f *File, data []byte) {
	t.Helper()
	for i := f.Chunks() - 1; i >= 0; i-- {
		start := i * ChunkSize
		if err := f.WriteChunk(i, data[start:start+f.ChunkLen(i)]); err != nil {
			t.Fatal(err)
		}
	}
}

func readAll(f *File) ([]byte, error) {
	var out []byte
	for i := range f.Chunks() {
		b, err := f.ReadChunk(i, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// A file written in any order reads back, after it is opened again, with
// every algorithm; one of chunks only is as long as what it holds.
func TestRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	for _, alg := range []Algorithm{Plain, AESGCM, ChaCha20Poly1305} {
		for _, size := range []int{0, 1, ChunkSize, 3*ChunkSize + 5} {
			path := filepath.Join(t.TempDir(), "f")
			data := content(size)
			f, err := Create(path, key, alg, int64(size), "c")
			if err != nil {
				t.Fatal(err)
			}
			writeAll(t, f, data)
			f.Close()
			if f, err = Open(path, key, "c"); err != nil {
				t.Fatal(alg, size, err)
			}
			got, err := readAll(f)
			f.Close()
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("%d, %d bytes: %v, equal %v", alg, size, err, bytes.Equal(got, data))
			}
			if alg != Plain && size >= 64 {
				raw, _ := os.ReadFile(path)
				if bytes.Contains(raw, data[:64]) {
					t.Fatalf("%d: the content is there as it is", alg)
				}
			}
		}
	}
}

// A chunk changed, moved to another place, copied from another file, not
// written, or a file cut short, its header changed or opened under another
// key, does not open.
func TestTampering(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	const size = 3*ChunkSize + 100
	data := content(size)
	sealed := func(t *testing.T, alg Algorithm) string {
		path := filepath.Join(t.TempDir(), "f")
		f, err := Create(path, key, alg, size, "c")
		if err != nil {
			t.Fatal(err)
		}
		writeAll(t, f, data)
		f.Close()
		return path
	}
	chunkAt := func(i int) int64 { return HeaderSize + int64(i)*(ChunkSize+Overhead) }
	for _, alg := range []Algorithm{AESGCM, ChaCha20Poly1305} {
		cases := map[string]func(t *testing.T, path string) (string, int){
			"changed": func(t *testing.T, path string) (string, int) {
				raw, _ := os.ReadFile(path)
				raw[chunkAt(1)+10] ^= 1
				os.WriteFile(path, raw, 0o600)
				return path, 1
			},
			"moved": func(t *testing.T, path string) (string, int) {
				raw, _ := os.ReadFile(path)
				copy(raw[chunkAt(2):chunkAt(3)], raw[chunkAt(1):chunkAt(2)])
				os.WriteFile(path, raw, 0o600)
				return path, 2
			},
			"from another file": func(t *testing.T, path string) (string, int) {
				other, _ := os.ReadFile(sealed(t, alg))
				raw, _ := os.ReadFile(path)
				copy(raw[chunkAt(1):chunkAt(2)], other[chunkAt(1):chunkAt(2)])
				os.WriteFile(path, raw, 0o600)
				return path, 1
			},
			"cut short": func(t *testing.T, path string) (string, int) {
				raw, _ := os.ReadFile(path)
				os.WriteFile(path, raw[:chunkAt(3)], 0o600)
				return path, 3
			},
			"shorter in the header": func(t *testing.T, path string) (string, int) {
				// The header says the file ends with its third chunk,
				// whole: the header is sealed with every chunk.
				raw, _ := os.ReadFile(path)
				raw[18] ^= 1
				os.WriteFile(path, raw, 0o600)
				return path, 0
			},
			"not written": func(t *testing.T, path string) (string, int) {
				f, _ := Create(path, key, alg, size, "c")
				f.WriteChunk(0, data[:ChunkSize])
				f.Close()
				return path, 1
			},
		}
		for name, tamper := range cases {
			t.Run(name, func(t *testing.T) {
				path, chunk := tamper(t, sealed(t, alg))
				f, err := Open(path, key, "c")
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if _, err := f.ReadChunk(chunk, nil); !errors.Is(err, ErrCorrupt) {
					t.Fatalf("%d: chunk %d: %v", alg, chunk, err)
				}
			})
		}
		t.Run("another context", func(t *testing.T) {
			f, err := Open(sealed(t, alg), key, "d")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.ReadChunk(0, nil); !errors.Is(err, ErrCorrupt) {
				t.Fatal(err)
			}
		})
		t.Run("another key", func(t *testing.T) {
			f, err := Open(sealed(t, alg), bytes.Repeat([]byte{8}, 32), "c")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.ReadChunk(0, nil); !errors.Is(err, ErrCorrupt) {
				t.Fatal(err)
			}
		})
	}
}

// Chunks of one file may be written and read at once.
func TestConcurrent(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	const chunks = 16
	data := content(chunks * ChunkSize)
	f, err := Create(filepath.Join(t.TempDir(), "f"), key, Fastest(), int64(len(data)), "c")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	done := make(chan error)
	for i := range chunks {
		go func() {
			err := f.WriteChunk(i, data[i*ChunkSize:(i+1)*ChunkSize])
			if err == nil {
				var b []byte
				if b, err = f.ReadChunk(i, nil); err == nil && !bytes.Equal(b, data[i*ChunkSize:(i+1)*ChunkSize]) {
					err = errors.New("wrong chunk")
				}
			}
			done <- err
		}()
	}
	for range chunks {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkChunk(b *testing.B) {
	key := bytes.Repeat([]byte{7}, 32)
	data := content(ChunkSize)
	for _, alg := range []Algorithm{Plain, AESGCM, ChaCha20Poly1305} {
		b.Run([]string{"plain", "aes-gcm", "chacha20-poly1305"}[alg], func(b *testing.B) {
			f, err := Create(filepath.Join(b.TempDir(), "f"), key, alg, 64*ChunkSize, "c")
			if err != nil {
				b.Fatal(err)
			}
			defer f.Close()
			buf := make([]byte, ChunkSize)
			b.SetBytes(ChunkSize * 2)
			i := 0
			for b.Loop() {
				if err := f.WriteChunk(i%64, data); err != nil {
					b.Fatal(err)
				}
				if buf, err = f.ReadChunk(i%64, buf); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})
	}
}
