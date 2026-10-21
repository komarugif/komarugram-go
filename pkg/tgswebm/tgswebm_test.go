// SPDX-License-Identifier: Unlicense OR MIT

package tgswebm

import (
	"context"
	"os"
	"testing"
	"time"

	"komarugram/pkg/lottie"
	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

// A TGS becomes a video sticker as Telegram's are: 512 pixels, 30 frames a
// second, as long as the animation, with alpha, within 256 KiB; its frames
// decode to the pictures the animation draws.
func TestConvert(t *testing.T) {
	ctx := context.Background()
	tgs, err := os.ReadFile("../../assets/stickers/sample.tgs")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	out, err := Convert(ctx, tgs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d bytes in %v", len(out), time.Since(started))
	if path := os.Getenv("TGSWEBM_OUT"); path != "" {
		os.WriteFile(path, out, 0o644)
	}
	file, err := webm.Demux(out)
	if err != nil {
		t.Fatal(err)
	}
	lr, _ := lottie.NewRuntime(ctx)
	defer lr.Close(ctx)
	anim, err := lr.Open(ctx, "sample", tgs, 512)
	if err != nil {
		t.Fatal(err)
	}
	defer anim.Close(ctx)
	length := time.Duration(float64(anim.FrameCount) / anim.FPS * float64(time.Second))
	if file.Width != 512 || file.Height != 512 || !file.HasAlpha() || file.FrameDuration != time.Second/30 ||
		(file.Duration()-length).Abs() > time.Second/30 || len(out) > 256<<10 {
		t.Fatalf("%dx%d, alpha %v, %v a frame, %v for %v, %d bytes", file.Width, file.Height, file.HasAlpha(), file.FrameDuration, file.Duration(), length, len(out))
	}
	dec, _ := vp9.NewRuntime(ctx)
	defer dec.Close(ctx)
	sticker, err := dec.OpenSticker(ctx, "converted", out)
	if err != nil {
		t.Fatal(err)
	}
	defer sticker.Close(ctx)
	for _, at := range []time.Duration{0, length / 2} {
		got, err := sticker.FrameAt(ctx, at+time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		want, _, err := anim.FrameAt(ctx, at+time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		var diff, n int
		for i := range want.Pix {
			d := int(got.Pix[i]) - int(want.Pix[i])
			diff += d * d
			n++
		}
		if mse := diff / n; mse > 40 {
			t.Errorf("at %v: mean squared error %d", at, mse)
		}
	}
}
