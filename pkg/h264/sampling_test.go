// SPDX-License-Identifier: Unlicense OR MIT

package h264

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"os"
	"testing"

	"komarugram/pkg/mp4"
)

// Pictures of 8-bit 4:4:4 and 4:2:2, which FFmpeg puts out as yuv444p and
// yuv422p, decode as they do in FFmpeg, brought to 4:2:0. Many old GIFs
// are High 4:4:4 Predictive, of odd widths. The files are FFmpeg's
// testsrc2, 6 pictures:
//
//	ffmpeg -f lavfi -i testsrc2=size=98x66:rate=30 -vf format=yuv444p,crop=97:65:0:0 -frames:v 6 -c:v libx264 -an testdata/avc444.mp4
//	ffmpeg -f lavfi -i testsrc2=size=96x66:rate=30 -vf format=yuv422p,crop=96:65:0:0 -frames:v 6 -c:v libx264 -an testdata/avc422.mp4
//
// and the sums are of FFmpeg's pictures (ffmpeg -i testdata/avc444.mp4
// -f rawvideo -pix_fmt yuv444p -), each its luma as it is and its chroma
// planes averaged in blocks of 2×2 (of 1×2 for 4:2:2) samples, rounded
// half up, those of the last column and row alone in theirs paired with
// themselves.
var sampled = []struct {
	file   string
	chroma int
	w, h   int
	sum    string
}{
	{"testdata/avc444.mp4", 3, 97, 65, "486987c9081d07690a30a57b083cc4147e8f6597077be9009b56393c5098aae4"},
	{"testdata/avc422.mp4", 2, 96, 65, "ce66bf7770351a2c63e99306a4c3f908d9e65da5f9ba271cdc44fb2705d39efb"},
}

// The parameter set tells how the pictures are sampled.
func TestReadAVCFormat(t *testing.T) {
	for _, c := range sampled {
		data, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		file, err := mp4.Demux(data)
		if err != nil {
			t.Fatal(err)
		}
		format, ok := mp4.ReadAVCFormat(file.Config)
		if want := (mp4.AVCFormat{Chroma: c.chroma, Depth: 8}); !ok || format != want {
			t.Errorf("%s: format %+v, %t; want %+v", c.file, format, ok, want)
		}
	}
}

func TestSampling(t *testing.T) {
	ctx := context.Background()
	path := os.Getenv("KOMARUGRAM_AVCDEC")
	if path == "" {
		t.Skip("set KOMARUGRAM_AVCDEC to an avcdec.wasm")
	}
	module, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(ctx, module)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	for _, c := range sampled {
		t.Run(c.file, func(t *testing.T) {
			data, err := os.ReadFile(c.file)
			if err != nil {
				t.Fatal(err)
			}
			v, err := rt.OpenVideo(ctx, data)
			if err != nil {
				t.Fatal(err)
			}
			defer v.Close(ctx)
			sum := sha256.New()
			for range v.Pictures() {
				if _, err := v.Next(ctx); err != nil {
					t.Fatal(err)
				}
				p := v.picture
				if p.SubsampleRatio != image.YCbCrSubsampleRatio420 || p.Rect != image.Rect(0, 0, c.w, c.h) {
					t.Fatalf("picture %v of %v, want %v of 4:2:0", p.Rect, p.SubsampleRatio, image.Rect(0, 0, c.w, c.h))
				}
				for y := range c.h {
					sum.Write(p.Y[y*p.YStride : y*p.YStride+c.w])
				}
				cw, ch := (c.w+1)/2, (c.h+1)/2
				for _, plane := range [][]byte{p.Cb, p.Cr} {
					for y := range ch {
						sum.Write(plane[y*p.CStride : y*p.CStride+cw])
					}
				}
			}
			if got := hex.EncodeToString(sum.Sum(nil)); got != c.sum {
				t.Fatalf("pictures sum to %s, FFmpeg's to %s", got, c.sum)
			}
		})
	}
}
