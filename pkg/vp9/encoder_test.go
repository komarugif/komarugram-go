// SPDX-License-Identifier: Unlicense OR MIT

package vp9

import (
	"context"
	"image"
	"os"
	"testing"
	"time"

	"komarugram/pkg/webm"
)

// picture is a premultiplied test picture: a gradient of colour under a
// gradient of alpha, which moves with t.
func picture(size, t int) *image.RGBA {
	im := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			a := uint32((x + t*4) % size * 255 / (size - 1))
			if y < size/4 {
				a = 255
			}
			if y >= size*3/4 {
				a = 0
			}
			r, g, b := uint32(x*255/size), uint32(y*255/size), uint32(200)
			p := im.Pix[y*im.Stride+x*4:]
			p[0], p[1], p[2], p[3] = byte(r*a/255), byte(g*a/255), byte(b*a/255), byte(a)
		}
	}
	return im
}

// Pictures with transparency encoded and muxed read back through Demux
// and the decoder as they were drawn, colour and alpha within what
// compression changes, frame for frame.
func TestAlphaEncodeRoundTrip(t *testing.T) {
	ctx := context.Background()
	rt, err := NewEncoderRuntime(ctx, EncoderLimits)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	const size, frames, fps = 128, 12, 30
	enc, err := rt.NewAlphaEncoder(ctx, EncoderConfig{Width: size, Height: size, FPS: fps, CQ: 20, Speed: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close(ctx)
	var muxed []webm.MuxFrame
	frameDuration := time.Second / fps
	take := func(color, alpha []Packet) {
		if len(color) != len(alpha) {
			t.Fatalf("%d colour packets, %d of alpha", len(color), len(alpha))
		}
		for i := range color {
			if color[i].Key != alpha[i].Key || color[i].PTS != alpha[i].PTS {
				t.Fatalf("colour %+v and alpha %+v out of step", color[i].PTS, alpha[i].PTS)
			}
			muxed = append(muxed, webm.MuxFrame{Data: color[i].Data, Alpha: alpha[i].Data, Key: color[i].Key, Timestamp: time.Duration(color[i].PTS) * frameDuration})
		}
	}
	started := time.Now()
	for i := range frames {
		color, alpha, err := enc.Encode(ctx, picture(size, i), int64(i), i%6 == 0)
		if err != nil {
			t.Fatal(err)
		}
		take(color, alpha)
	}
	color, alpha, err := enc.Flush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	take(color, alpha)
	t.Logf("%d frames of %dx%d in %v", frames, size, size, time.Since(started))
	if len(muxed) != frames || !muxed[0].Key || !muxed[6].Key || muxed[1].Key {
		t.Fatalf("%d frames, keys %v %v %v", len(muxed), muxed[0].Key, muxed[6].Key, muxed[1].Key)
	}
	data, err := webm.Mux(size, size, frameDuration, muxed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d bytes", len(data))
	// VP9_ENCODE_OUT keeps the file, to look at with other programs.
	if out := os.Getenv("VP9_ENCODE_OUT"); out != "" {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	file, err := webm.Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Width != size || file.Height != size || len(file.Frames) != frames || !file.HasAlpha() || file.FrameDuration != frameDuration {
		t.Fatalf("demuxed %dx%d, %d frames, alpha %v, %v", file.Width, file.Height, len(file.Frames), file.HasAlpha(), file.FrameDuration)
	}
	dec, err := NewRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close(ctx)
	sticker, err := dec.OpenSticker(ctx, "test", data)
	if err != nil {
		t.Fatal(err)
	}
	defer sticker.Close(ctx)
	if !sticker.HasAlpha() {
		t.Fatal("no alpha decoded")
	}
	for _, i := range []int{0, 3, 6, 11} {
		got, err := sticker.FrameAt(ctx, time.Duration(i)*frameDuration+frameDuration/2)
		if err != nil {
			t.Fatal(err)
		}
		want := picture(size, i)
		var diff, n int
		for j := range want.Pix {
			d := int(got.Pix[j]) - int(want.Pix[j])
			diff += d * d
			n++
		}
		if mse := diff / n; mse > 30 {
			t.Errorf("frame %d: mean squared error %d", i, mse)
		}
		// Opaque and clear rows come back opaque and clear.
		if a := got.Pix[(2)*got.Stride+size/2*4+3]; a < 245 {
			t.Errorf("frame %d: an opaque pixel of alpha %d", i, a)
		}
		if a := got.Pix[(size-2)*got.Stride+size/2*4+3]; a > 10 {
			t.Errorf("frame %d: a clear pixel of alpha %d", i, a)
		}
	}
}
