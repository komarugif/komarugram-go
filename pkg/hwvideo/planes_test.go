// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"testing"
)

// splitUVBytes is splitUV a byte at a time, as the readback did.
func splitUVBytes(cb, cr, uv []byte) {
	for x := range cb {
		cb[x], cr[x] = uv[2*x], uv[2*x+1]
	}
}

func TestSplitUV(t *testing.T) {
	for n := range 21 {
		uv := make([]byte, 2*n+3)
		for i := range uv {
			uv[i] = byte(i*37 + 11)
		}
		cb, cr := make([]byte, n), make([]byte, n)
		wantCb, wantCr := make([]byte, n), make([]byte, n)
		splitUV(cb, cr, uv)
		splitUVBytes(wantCb, wantCr, uv)
		if string(cb) != string(wantCb) || string(cr) != string(wantCr) {
			t.Fatalf("%d samples: %v %v, want %v %v", n, cb, cr, wantCb, wantCr)
		}
	}
}

// The chroma of a picture of 720x1280.
func benchmarkSplit(b *testing.B, split func(cb, cr, uv []byte)) {
	uv := make([]byte, 720*640)
	cb, cr := make([]byte, 360*640), make([]byte, 360*640)
	b.SetBytes(int64(len(uv)))
	for b.Loop() {
		for y := range 640 {
			split(cb[y*360:y*360+360], cr[y*360:], uv[y*720:])
		}
	}
}

func BenchmarkSplitUV(b *testing.B)      { benchmarkSplit(b, splitUV) }
func BenchmarkSplitUVBytes(b *testing.B) { benchmarkSplit(b, splitUVBytes) }
