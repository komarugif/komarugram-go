// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux && cgo && !novulkan

package hwvideo

import (
	"errors"
	"fmt"
	"image"
	"math/rand/v2"
	"testing"

	"komarugram/pkg/resample"
)

// testNV12 is a picture of pitch×rows to scale: gradients, noise and hard
// edges, which a filter that differs shows.
func testNV12(pitch, rows int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	out := make([]byte, pitch*rows*3/2)
	for y := range rows {
		for x := range pitch {
			v := (x*255/pitch + y*255/rows) / 2
			if (x/7+y/11)%5 == 0 {
				v = 255 - v
			}
			out[y*pitch+x] = byte(v + r.IntN(17) - 8)
		}
	}
	uv := out[pitch*rows:]
	for y := range rows / 2 {
		for x := range pitch / 2 {
			uv[y*pitch+2*x] = byte(x*255/(pitch/2)/2 + 64 + r.IntN(9))
			uv[y*pitch+2*x+1] = byte(255 - y*255/(rows/2) + r.IntN(9) - 4)
			if (x/5+y/3)%4 == 0 {
				uv[y*pitch+2*x+1] = 16
			}
		}
	}
	return out
}

// planesOf is the picture of width×height at (left, top) of an NV12 one,
// as a session reads it.
func planesOf(nv12 []byte, pitch, rows, left, top, width, height int) *image.YCbCr {
	im := new(image.YCbCr)
	sizeYCbCr(im, width, height)
	for y := range height {
		copy(im.Y[y*im.YStride:y*im.YStride+width], nv12[(y+top)*pitch+left:])
	}
	cw, ch := (width+1)/2, (height+1)/2
	chroma := nv12[pitch*rows:]
	for y := range ch {
		splitUV(im.Cb[y*im.CStride:y*im.CStride+cw], im.Cr[y*im.CStride:y*im.CStride+cw], chroma[(y+top/2)*pitch+left/2*2:])
	}
	return im
}

// scaleCase is a picture of a size scaled to another.
type scaleCase struct {
	pitch, rows, left, top, width, height int
	w, h                                  int
	colors                                resample.Colors
	rough                                 bool
}

var scaleCases = []scaleCase{
	{pitch: 720, rows: 1280, width: 720, height: 1280, w: 160, h: 284, colors: resample.Colors{Matrix: resample.BT709, Limited: true}},
	{pitch: 720, rows: 1280, width: 720, height: 1280, w: 160, h: 284, colors: resample.Colors{Matrix: resample.BT709, Limited: true}, rough: true},
	{pitch: 720, rows: 1280, width: 720, height: 1280, w: 72, h: 128, colors: resample.Colors{Matrix: resample.BT601, Limited: true}, rough: true},
	{pitch: 640, rows: 368, width: 636, height: 358, w: 100, h: 57, colors: resample.Colors{Matrix: resample.BT601, Limited: true}},
	{pitch: 640, rows: 368, left: 2, top: 4, width: 630, height: 358, w: 211, h: 120, colors: resample.Colors{Matrix: resample.BT709}},
	{pitch: 336, rows: 256, width: 333, height: 251, w: 160, h: 121, colors: resample.Colors{Matrix: resample.BT2020, Limited: true}, rough: true},
	{pitch: 1920, rows: 1088, width: 1920, height: 1080, w: 20, h: 12, colors: resample.Colors{Matrix: resample.BT709, Limited: true}},
	{pitch: 64, rows: 48, width: 64, height: 48, w: 100, h: 75, colors: resample.Colors{Matrix: resample.BT601, Limited: true}},
	{pitch: 64, rows: 48, width: 64, height: 48, w: 64, h: 48, colors: resample.Colors{Matrix: resample.BT709, Limited: true}},
	{pitch: 64, rows: 48, width: 64, height: 48, w: 64, h: 48},
}

// TestScalerAsCPU scales pictures from the host with the GPU's scaler, on
// any Vulkan device (lavapipe too), and compares them with the CPU's
// scaler: the same filters, so they differ only by rounding.
func TestScalerAsCPU(t *testing.T) {
	c, err := newComputeScaler()
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	for i, tc := range scaleCases {
		t.Run(fmt.Sprintf("%dx%d-to-%dx%d-%d", tc.width, tc.height, tc.w, tc.h, i), func(t *testing.T) {
			nv12 := testNV12(tc.pitch, tc.rows, uint64(i))
			scaler := resample.VideoScaler{Colors: tc.colors, Rough: tc.rough}
			want := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
			scaler.ResizeInto(want, planesOf(nv12, tc.pitch, tc.rows, tc.left, tc.top, tc.width, tc.height), nil)
			got := image.NewRGBA(want.Rect)
			plan := scaler.Plan(tc.width, tc.height, tc.w, tc.h)
			if err := c.scale(nv12, tc.pitch, tc.rows, tc.left, tc.top, tc.width, tc.height, plan, got); err != nil {
				t.Fatal(err)
			}
			largest, mean := difference(got, want)
			t.Logf("plan %+v: largest difference %d, mean %.4f", plan, largest, mean)
			if largest > 2 {
				t.Errorf("the GPU's picture differs from the CPU's by %d (mean %.4f)", largest, mean)
			}
		})
	}
}
