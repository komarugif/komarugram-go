// SPDX-License-Identifier: Unlicense OR MIT

package h264

import (
	"bytes"
	"context"
	"os"
	"testing"
)

// A seek to a picture's time goes back to the keyframe before it, and
// decoding on from there gives the very picture decoding from the start
// does, at the same index.
func TestSeek(t *testing.T) {
	ctx := context.Background()
	path := os.Getenv("KOMARUGRAM_AVCDEC")
	if path == "" {
		t.Skip("set KOMARUGRAM_AVCDEC to an avcdec.wasm")
	}
	module, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(ctx, module)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	luma := func(v *Video) []byte {
		p := v.picture
		var b bytes.Buffer
		for y := range p.Rect.Dy() {
			b.Write(p.Y[y*p.YStride : y*p.YStride+p.Rect.Dx()])
		}
		return b.Bytes()
	}
	want := map[int][]byte{}
	through, err := rt.OpenVideo(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer through.Close(ctx)
	targets := []int{700, 300, 0}
	for i := 0; i <= 700; i++ {
		idx, err := through.Next(ctx)
		if err != nil || idx != i {
			t.Fatalf("picture %d came as %d: %v", i, idx, err)
		}
		for _, target := range targets {
			if i == target {
				want[i] = luma(through)
			}
		}
	}
	v, err := rt.OpenVideo(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(ctx)
	for _, target := range targets {
		k, err := v.Seek(ctx, v.Time(target))
		if err != nil {
			t.Fatal(err)
		}
		if k > target || !v.File.Samples[k].Keyframe {
			t.Fatalf("seek to %d went to %d, not a keyframe before it", target, k)
		}
		idx := k - 1
		for idx < target {
			if idx, err = v.Next(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if idx != target || !bytes.Equal(luma(v), want[target]) {
			t.Fatalf("seek to %d (keyframe %d) decoded picture %d unlike the one decoded through", target, k, idx)
		}
	}
}
