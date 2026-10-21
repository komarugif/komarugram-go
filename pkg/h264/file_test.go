// SPDX-License-Identifier: Unlicense OR MIT

package h264

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"komarugram/pkg/mp4"
)

// A video read where it lies decodes the pictures the one read whole does,
// from the start and from a seek.
func TestOpenFileReadsWhereItLies(t *testing.T) {
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
	whole, err := rt.OpenVideo(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close(ctx)
	file, err := mp4.DemuxAt(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	at, err := rt.OpenFile(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer at.Close(ctx)
	for i := range 40 {
		a, errA := whole.Next(ctx)
		b, errB := at.Next(ctx)
		if errA != nil || errB != nil || a != b || !bytes.Equal(luma(whole), luma(at)) {
			t.Fatalf("picture %d: %d %v, %d %v, or they differ", i, a, errA, b, errB)
		}
	}
	for _, v := range []*Video{whole, at} {
		if _, err := v.Seek(ctx, 20*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 40 {
		a, errA := whole.Next(ctx)
		b, errB := at.Next(ctx)
		if errA != nil || errB != nil || a != b || !bytes.Equal(luma(whole), luma(at)) {
			t.Fatalf("picture %d after the seek: %d %v, %d %v, or they differ", i, a, errA, b, errB)
		}
	}
}
