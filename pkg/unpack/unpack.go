// SPDX-License-Identifier: Unlicense OR MIT

// Package unpack unpacks compressed data whole, into one buffer of its
// size: zstd, which the program embeds its modules and data in, and gzip,
// which Telegram sends stickers and patterns in.
package unpack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// ErrTooLarge is returned for data that unpacks to more than its limit.
var ErrTooLarge = errors.New("unpack: data larger than its limit")

// maxEmbedded bounds what a zstd frame may unpack to: the largest module
// embedded is about 2 MB.
const maxEmbedded = 64 << 20

var decoder = sync.OnceValues(func() (*zstd.Decoder, error) {
	// Two at once: modules are compiled as they are first needed, seldom
	// together. Each frame carries its size, so DecodeAll unpacks into
	// one buffer of it.
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(2), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(maxEmbedded))
})

// Zstd unpacks zstd-compressed data, as the program embeds it
// (`zstd --ultra -22`).
func Zstd(src []byte) ([]byte, error) {
	d, err := decoder()
	if err != nil {
		return nil, err
	}
	return d.DecodeAll(src, nil)
}

// Gzip unpacks gzip-compressed data of at most limit bytes unpacked. The
// buffer takes the size the gzip trailer gives, within limit, so that the
// data is not copied over as a buffer grows: io.ReadAll took two and a half
// times the size.
func Gzip(src []byte, limit int) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	size := 0
	if len(src) >= 4 {
		// The size modulo 2³², of the last member only: a hint, from
		// whoever made the data.
		size = int(min(binary.LittleEndian.Uint32(src[len(src)-4:]), uint32(min(limit, 1<<31-1))))
	}
	out := make([]byte, size)
	n, err := io.ReadFull(r, out)
	out = out[:n]
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return out, nil
	case err != nil:
		return nil, err
	}
	// The data may go on past the size the trailer gave: several members,
	// or a trailer that does not tell the truth.
	var probe [1]byte
	for {
		k, err := r.Read(probe[:])
		if k > 0 {
			break
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
	rest, err := io.ReadAll(io.LimitReader(r, int64(limit-len(out))))
	if err != nil {
		return nil, err
	}
	out = append(append(out, probe[0]), rest...)
	if len(out) > limit {
		return nil, ErrTooLarge
	}
	return out, nil
}
