// SPDX-License-Identifier: Unlicense OR MIT

// Package sealedfile keeps a file encrypted in chunks that are written and
// read apart, in any order, so that a download can fill it as its parts come
// and a player can read where it seeks.
//
// Each chunk is sealed with an AEAD under a key of the file's own, derived
// from the caller's key, a salt of the file's and the caller's context, which
// names what the file holds: a file copied in place of another, sealed under
// the same key, does not open. The chunk's index is the
// nonce, and the header, the index and whether the chunk is the last are
// authenticated with it. A chunk changed, moved to another place or file, or
// a file cut short or its header changed, fails to open instead of giving
// other bytes: the STREAM construction of age and Tink, with chunks at fixed
// places.
//
// A file is a header, then chunk i at HeaderSize + i×(ChunkSize+Overhead).
// Chunks not written yet are holes the file system may keep sparse; which
// are there is the caller's to keep.
package sealedfile

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/sys/cpu"
)

const (
	// ChunkSize is the size of a chunk: a part of a Telegram download.
	ChunkSize = 128 << 10
	// Overhead is what sealing adds to a chunk.
	Overhead = 16
	// HeaderSize is the size of the header.
	HeaderSize = 64
)

// Algorithm is how a file's chunks are sealed.
type Algorithm byte

const (
	// Plain keeps chunks as they are, for a cache the user keeps without
	// protection: the same layout, without the overhead.
	Plain Algorithm = iota
	// AESGCM is AES-256-GCM, fast where the processor has AES instructions.
	AESGCM
	// ChaCha20Poly1305 is constant-time in software, for processors without
	// them.
	ChaCha20Poly1305
)

var (
	// ErrCorrupt is a chunk or header that does not open: changed, moved,
	// not written yet, or sealed under another key.
	ErrCorrupt = errors.New("sealedfile: corrupt or foreign chunk")
	magic      = [4]byte{'K', 'G', 'M', '1'}
)

// Fastest is the algorithm the processor seals fastest with constant-time
// code: AES-GCM where it has AES and carry-less multiplication instructions.
func Fastest() Algorithm {
	switch runtime.GOARCH {
	case "amd64":
		if cpu.X86.HasAES && cpu.X86.HasPCLMULQDQ {
			return AESGCM
		}
	case "arm64":
		if cpu.ARM64.HasAES && cpu.ARM64.HasPMULL {
			return AESGCM
		}
	}
	return ChaCha20Poly1305
}

// File is a sealed file open for reading and writing chunks. Its methods
// may be called at once from several goroutines.
type File struct {
	f      *os.File
	header [HeaderSize]byte
	aead   cipher.AEAD
	size   int64
}

// Create makes the file at path for size bytes, sealed with alg under key (32
// bytes; none for Plain) for context, replacing what was there.
func Create(path string, key []byte, alg Algorithm, size int64, context string) (*File, error) {
	if size < 0 {
		return nil, fmt.Errorf("sealedfile: size %d", size)
	}
	var h [HeaderSize]byte
	copy(h[:], magic[:])
	h[4] = byte(alg)
	binary.BigEndian.PutUint32(h[8:], ChunkSize)
	binary.BigEndian.PutUint64(h[12:], uint64(size))
	if alg != Plain {
		if _, err := rand.Read(h[20:52]); err != nil {
			return nil, err
		}
	}
	file := &File{header: h, size: size}
	if err := file.init(key, context); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteAt(h[:], 0); err != nil {
		f.Close()
		return nil, err
	}
	file.f = f
	return file, nil
}

// Open opens the file at path, sealed under key for context.
func Open(path string, key []byte, context string) (*File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	file := &File{f: f}
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, HeaderSize), file.header[:]); err != nil {
		f.Close()
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, ErrCorrupt
		}
		return nil, err
	}
	h := file.header
	if [4]byte(h[:4]) != magic || binary.BigEndian.Uint32(h[8:]) != ChunkSize {
		f.Close()
		return nil, ErrCorrupt
	}
	file.size = int64(binary.BigEndian.Uint64(h[12:]))
	if file.size < 0 {
		f.Close()
		return nil, ErrCorrupt
	}
	if err := file.init(key, context); err != nil {
		f.Close()
		return nil, err
	}
	return file, nil
}

// init makes the AEAD of the file's algorithm, under the file's key.
func (f *File) init(key []byte, context string) error {
	alg := Algorithm(f.header[4])
	if alg == Plain {
		return nil
	}
	if len(key) != 32 {
		return fmt.Errorf("sealedfile: a key of %d bytes", len(key))
	}
	fileKey, err := hkdf.Key(sha256.New, key, f.header[20:52], fmt.Sprintf("komarugram sealedfile %d %s", alg, context), 32)
	if err != nil {
		return err
	}
	switch alg {
	case AESGCM:
		block, err := aes.NewCipher(fileKey)
		if err != nil {
			return err
		}
		f.aead, err = cipher.NewGCM(block)
		return err
	case ChaCha20Poly1305:
		f.aead, err = chacha20poly1305.New(fileKey)
		return err
	}
	return ErrCorrupt
}

// Algorithm is how the file is sealed.
func (f *File) Algorithm() Algorithm { return Algorithm(f.header[4]) }

// Size is the size of what the file holds, once all of it is written.
func (f *File) Size() int64 { return f.size }

// Chunks is the number of chunks of the file.
func (f *File) Chunks() int { return int((f.size + ChunkSize - 1) / ChunkSize) }

// ChunkLen is the length of chunk i: ChunkSize but for the last.
func (f *File) ChunkLen(i int) int {
	return int(min(ChunkSize, f.size-int64(i)*ChunkSize))
}

// place is where chunk i is in the file.
func (f *File) place(i int) int64 {
	stride := int64(ChunkSize)
	if f.aead != nil {
		stride += Overhead
	}
	return HeaderSize + int64(i)*stride
}

// sealedChunks are buffers for sealed chunks: a chunk read or written does
// not allocate one.
var sealedChunks = sync.Pool{New: func() any { b := make([]byte, ChunkSize+Overhead); return &b }}

// nonce and data are the nonce and the authenticated data of chunk i. Both
// algorithms take a nonce of 12 bytes.
func (f *File) nonce(i int) (nonce [12]byte, data [HeaderSize + 9]byte) {
	binary.BigEndian.PutUint64(nonce[4:], uint64(i))
	copy(data[:], f.header[:])
	binary.BigEndian.PutUint64(data[HeaderSize:], uint64(i))
	if i == f.Chunks()-1 {
		data[HeaderSize+8] = 1
	}
	return nonce, data
}

// WriteChunk seals data, all of chunk i, into its place.
func (f *File) WriteChunk(i int, data []byte) error {
	if i < 0 || i >= f.Chunks() || len(data) != f.ChunkLen(i) {
		return fmt.Errorf("sealedfile: chunk %d of %d bytes", i, len(data))
	}
	if f.aead == nil {
		_, err := f.f.WriteAt(data, f.place(i))
		return err
	}
	buf := sealedChunks.Get().(*[]byte)
	defer sealedChunks.Put(buf)
	nonce, ad := f.nonce(i)
	sealed := f.aead.Seal((*buf)[:0], nonce[:], data, ad[:])
	_, err := f.f.WriteAt(sealed, f.place(i))
	return err
}

// ReadChunk opens chunk i into buf, which it returns resliced, or grown
// when it holds less than ChunkSize.
func (f *File) ReadChunk(i int, buf []byte) ([]byte, error) {
	if i < 0 || i >= f.Chunks() {
		return nil, fmt.Errorf("sealedfile: chunk %d of %d", i, f.Chunks())
	}
	n := f.ChunkLen(i)
	if f.aead == nil {
		buf = grow(buf, n)
		if _, err := f.f.ReadAt(buf, f.place(i)); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, ErrCorrupt
			}
			return nil, err
		}
		return buf, nil
	}
	pooled := sealedChunks.Get().(*[]byte)
	defer sealedChunks.Put(pooled)
	sealed := (*pooled)[:n+Overhead]
	if _, err := f.f.ReadAt(sealed, f.place(i)); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrCorrupt
		}
		return nil, err
	}
	nonce, ad := f.nonce(i)
	out, err := f.aead.Open(grow(buf, n)[:0], nonce[:], sealed, ad[:])
	if err != nil {
		return nil, ErrCorrupt
	}
	return out, nil
}

func grow(buf []byte, n int) []byte {
	if cap(buf) < n {
		return make([]byte, n)
	}
	return buf[:n]
}

// Sync commits what was written to the disk.
func (f *File) Sync() error { return f.f.Sync() }

// Close closes the file.
func (f *File) Close() error { return f.f.Close() }
