// SPDX-License-Identifier: Unlicense OR MIT

package mp4

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"slices"
)

// Patch is what goes over a file at Offset: Data, then Zeros zero bytes.
type Patch struct {
	Offset int64
	Data   []byte
	Zeros  int64
}

func (p Patch) end() int64 { return p.Offset + int64(len(p.Data)) + p.Zeros }

// Patched reads the size bytes of r with patches, sorted and apart, over them.
func Patched(r io.ReaderAt, size int64, patches []Patch) io.Reader {
	return &patched{r: r, size: size, patches: patches}
}

type patched struct {
	r       io.ReaderAt
	size    int64
	at      int64
	patches []Patch
}

func (p *patched) Read(b []byte) (int, error) {
	if p.at >= p.size {
		return 0, io.EOF
	}
	b = b[:min(int64(len(b)), p.size-p.at)]
	n, err := p.r.ReadAt(b, p.at)
	if n < len(b) && err == nil {
		err = io.ErrUnexpectedEOF
	}
	b = b[:n]
	from, to := p.at, p.at+int64(n)
	// The patches before this read are done with.
	for len(p.patches) > 0 && p.patches[0].end() <= from {
		p.patches = p.patches[1:]
	}
	for _, q := range p.patches {
		if q.Offset >= to {
			break
		}
		for i, c := range q.Data {
			if at := q.Offset + int64(i); at >= from && at < to {
				b[at-from] = c
			}
		}
		lo, hi := max(q.Offset+int64(len(q.Data)), from), min(q.end(), to)
		if lo < hi {
			clear(b[lo-from : hi-from])
		}
	}
	p.at = to
	if err == io.EOF && p.at < p.size {
		err = io.ErrUnexpectedEOF
	} else if err == io.EOF {
		err = nil
	}
	return n, err
}

// xmpUUID is the extended type of the box Adobe's XMP is kept in.
var xmpUUID = []byte{0xbe, 0x7a, 0xcf, 0xcb, 0x97, 0xa9, 0x42, 0xe8, 0x9c, 0x71, 0x99, 0x94, 0x91, 0xe3, 0xaf, 0xac}

// maxMovieBox is the largest movie box read to scrub: its tables grow with the
// length of the video, a few megabytes for hours of it.
const maxMovieBox = 256 << 20

// MetadataPatches finds what in the ISO media file r of size tells where, when
// and with what it was made, for a file sent to others not to tell it: the user
// data and metadata boxes (udta, meta: a phone's location, its make and model,
// the date, the software) of the movie and its tracks, and XMP, become free
// boxes of zeros, and the times of creation and modification of the movie, its
// tracks and their media become zero. What plays stays as it is: the file keeps
// its size and every offset in it, so that the patches need no copy of it. A
// file with nothing to scrub has none.
func MetadataPatches(r io.ReaderAt, size int64) ([]Patch, error) {
	var patches []Patch
	for at := int64(0); at < size; {
		kind, header, boxSize, err := readBoxHeader(r, at, size)
		if err != nil {
			return nil, err
		}
		switch kind {
		case "moov":
			if boxSize-header > maxMovieBox {
				return nil, fmt.Errorf("mp4: movie box of %d bytes", boxSize)
			}
			body := make([]byte, boxSize-header)
			if _, err := r.ReadAt(body, at+header); err != nil {
				return nil, err
			}
			found, err := scrubContainer(body, at+header, "moov")
			if err != nil {
				return nil, err
			}
			patches = append(patches, found...)
		case "udta", "meta":
			patches = append(patches, freeBox(at, header, boxSize)...)
		case "uuid":
			id := make([]byte, 16)
			if boxSize-header >= 16 {
				if _, err := r.ReadAt(id, at+header); err != nil {
					return nil, err
				}
				if bytes.Equal(id, xmpUUID) {
					patches = append(patches, freeBox(at, header, boxSize)...)
				}
			}
		}
		at += boxSize
	}
	slices.SortFunc(patches, func(a, b Patch) int { return int(a.Offset - b.Offset) })
	return patches, nil
}

// readBoxHeader reads the header of the box at at of a file of size: its type,
// the length of the header and the box's whole size.
func readBoxHeader(r io.ReaderAt, at, size int64) (kind string, header, boxSize int64, err error) {
	var h [16]byte
	if size-at < 8 {
		return "", 0, 0, fmt.Errorf("mp4: truncated box header at %d", at)
	}
	if _, err := r.ReadAt(h[:8], at); err != nil {
		return "", 0, 0, err
	}
	kind, header, boxSize = string(h[4:8]), 8, int64(binary.BigEndian.Uint32(h[:4]))
	switch boxSize {
	case 0:
		boxSize = size - at
	case 1:
		if size-at < 16 {
			return "", 0, 0, fmt.Errorf("mp4: truncated %q header", kind)
		}
		if _, err := r.ReadAt(h[8:16], at+8); err != nil {
			return "", 0, 0, err
		}
		header, boxSize = 16, int64(binary.BigEndian.Uint64(h[8:16]))
	}
	if boxSize < header || boxSize > size-at {
		return "", 0, 0, fmt.Errorf("mp4: %q box of %d bytes at %d in %d", kind, boxSize, at, size)
	}
	return kind, header, boxSize, nil
}

// scrubContainer finds the patches of the boxes of body, the body of a box of
// type parent, at offset base in the file.
func scrubContainer(body []byte, base int64, parent string) ([]Patch, error) {
	var patches []Patch
	for at := 0; at < len(body); {
		rest := body[at:]
		if len(rest) < 8 {
			return nil, fmt.Errorf("mp4: truncated box header in %q", parent)
		}
		size, header := int(binary.BigEndian.Uint32(rest)), 8
		kind := string(rest[4:8])
		switch size {
		case 0:
			size = len(rest)
		case 1:
			if len(rest) < 16 {
				return nil, fmt.Errorf("mp4: truncated %q header", kind)
			}
			big := binary.BigEndian.Uint64(rest[8:16])
			if big > uint64(len(rest)) {
				return nil, fmt.Errorf("mp4: %q box of %d bytes in %d", kind, big, len(rest))
			}
			size, header = int(big), 16
		}
		if size < header || size > len(rest) {
			return nil, fmt.Errorf("mp4: %q box of %d bytes in %d", kind, size, len(rest))
		}
		offset := base + int64(at)
		inner := rest[header:size]
		switch kind {
		case "udta", "meta":
			patches = append(patches, freeBox(offset, int64(header), int64(size))...)
		case "uuid":
			if bytes.HasPrefix(inner, xmpUUID) {
				patches = append(patches, freeBox(offset, int64(header), int64(size))...)
			}
		case "trak", "mdia":
			found, err := scrubContainer(inner, offset+int64(header), kind)
			if err != nil {
				return nil, err
			}
			patches = append(patches, found...)
		case "mvhd", "tkhd", "mdhd":
			if p, ok := zeroTimes(inner, offset+int64(header)); ok {
				patches = append(patches, p)
			}
		}
		at += size
	}
	return patches, nil
}

// freeBox makes the box at offset, of header and size, a free box of zeros: its
// type, and what follows its header; a large box's 64-bit size stays.
func freeBox(offset, header, size int64) []Patch {
	if header == 8 {
		return []Patch{{Offset: offset + 4, Data: []byte("free"), Zeros: size - header}}
	}
	return []Patch{{Offset: offset + 4, Data: []byte("free")}, {Offset: offset + header, Zeros: size - header}}
}

// zeroTimes is the patch that makes the creation and modification times of a
// movie, track or media header body, at offset, zero: after its version and
// flags, 4 bytes each in version 0, 8 in version 1.
func zeroTimes(body []byte, offset int64) (Patch, bool) {
	if len(body) < 4 {
		return Patch{}, false
	}
	n := int64(8)
	if body[0] == 1 {
		n = 16
	}
	if int64(len(body)) < 4+n {
		return Patch{}, false
	}
	return Patch{Offset: offset + 4, Zeros: n}, true
}
