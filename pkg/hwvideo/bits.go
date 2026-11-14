// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import "math/bits"

// unescape returns the payload of a NAL unit without its emulation
// prevention bytes (the 3 of 0, 0, 3), at most limit bytes of it.
func unescape(nal []byte, limit int) []byte {
	out := make([]byte, 0, min(len(nal), limit))
	zeros := 0
	for _, b := range nal {
		if len(out) == limit {
			break
		}
		if zeros >= 2 && b == 3 {
			zeros = 0
			continue
		}
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, b)
	}
	return out
}

// bitReader reads the syntax elements of a payload without emulation
// prevention bytes. Reading past the end sets failed and gives zeros, so
// that a parser checks once, after it read what it wanted.
type bitReader struct {
	data   []byte
	pos    int // in bits
	failed bool
}

func (r *bitReader) bit() uint32 {
	if r.pos >= len(r.data)*8 {
		r.failed = true
		return 0
	}
	b := r.data[r.pos>>3] >> (7 - r.pos&7) & 1
	r.pos++
	return uint32(b)
}

func (r *bitReader) flag() bool { return r.bit() == 1 }

// u reads n bits, at most 32.
func (r *bitReader) u(n int) uint32 {
	var v uint32
	for range n {
		v = v<<1 | r.bit()
	}
	return v
}

// ue reads an Exp-Golomb code, which fails if it is over 32 bits.
func (r *bitReader) ue() uint32 {
	zeros := 0
	for r.bit() == 0 {
		if r.failed {
			return 0
		}
		zeros++
		if zeros > 32 {
			r.failed = true
			return 0
		}
	}
	if zeros == 32 {
		// Only 2^32-1 fits, coded with 32 zeros after the one.
		if r.u(32) != 0 {
			r.failed = true
		}
		return 1<<32 - 1
	}
	return 1<<zeros - 1 + r.u(zeros)
}

// ueMax reads an Exp-Golomb code that may be at most limit.
func (r *bitReader) ueMax(limit uint32) uint32 {
	v := r.ue()
	if v > limit {
		r.failed = true
		return 0
	}
	return v
}

// se reads a signed Exp-Golomb code.
func (r *bitReader) se() int32 {
	v := r.ue()
	if v == 1<<32-1 {
		r.failed = true
		return 0
	}
	if v&1 == 1 {
		return int32(v/2 + 1)
	}
	return -int32(v / 2)
}

// more reports whether syntax elements are left before the trailing bits
// of the payload (more_rbsp_data).
func (r *bitReader) more() bool {
	last := len(r.data) - 1
	for last >= 0 && r.data[last] == 0 {
		last--
	}
	if last < 0 {
		return false
	}
	stop := last*8 + 7 - bits.TrailingZeros8(r.data[last])
	return r.pos < stop
}
