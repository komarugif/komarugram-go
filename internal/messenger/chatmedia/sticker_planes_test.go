// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"context"
	"image"
	"math"
	"os"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

// A sticker of three seconds at 60 frames a second, in a message at a scale
// of 1, is too large a loop as pictures, and fits in planes.
func TestFastStickerLoopFitsInPlanes(t *testing.T) {
	sticker := &vp9.Sticker{File: &webm.File{Frames: make([]webm.Frame, 180), FrameDuration: time.Second / 60}, Size: image.Pt(512, 512)}
	box := image.Pt(192, 192)
	if stickerClipFits(sticker, box) {
		t.Fatal("180 pictures of a message's sticker are over the cap")
	}
	if !stickerPlanesFit(sticker, box) {
		t.Fatal("the loop of a sticker at 60 frames a second should fit in planes")
	}
	if stickerPlanesFit(sticker, image.Pt(256, 256)) {
		t.Fatal("a larger loop must stay under the cap in planes too")
	}
	if !stickerClipFits(sticker, image.Pt(128, 128)) {
		t.Fatal("in a cell of a panel the loop fits as pictures")
	}
}

// TestStickerPlaysFromPlanes plays a sticker in a view too large for a loop
// of pictures: its loop is kept in planes, with no decoder left, and the
// pictures made of it are those the decoder gives.
func TestStickerPlaysFromPlanes(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Skip("no sticker to decode")
	}
	m := New(&fakeSource{data: data}, func() {})
	m.ConfigureDecoders(true, false, "")
	defer m.Close()
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "planes-webm", MIMEType: "video/webm", Width: 512, Height: 512}}
	// 30 frames of 400×400: 19 MB as pictures, 12 in planes.
	view := image.Pt(400, 400)
	var clip *stickerClip
	// seen are the pictures shown once the loop is there.
	seen := map[*image.RGBA]bool{}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && len(seen) < 3 {
		m.BeginFrame()
		status := m.StatusFit(msg, true, view, false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		e := m.entries[msg.Media.ID]
		e.mu.Lock()
		clip = e.clip
		e.mu.Unlock()
		if clip != nil {
			if im, ok := status.Frame.(*image.RGBA); ok && im != nil {
				if im.Rect.Size() != view {
					t.Fatalf("picture of %v, want %v", im.Rect.Size(), view)
				}
				seen[im] = true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if clip == nil {
		t.Fatal("the sticker did not finish its loop")
	}
	if len(clip.frames) != 0 || len(clip.planes) != 30 || len(clip.colors) != 30 {
		t.Fatalf("loop of %d pictures and %d frames in planes, want 30 in planes", len(clip.frames), len(clip.planes))
	}
	if want := 30 * planesBytes(view); clip.bytes != want || clip.bytes > maxStickerClipBytes {
		t.Fatalf("loop of %d bytes, want %d", clip.bytes, want)
	}
	if len(seen) < 3 {
		t.Fatalf("%d pictures shown from the loop: it does not play", len(seen))
	}
	if used := m.vp9.current.rt.Budget().Used(); used != 0 {
		t.Fatalf("decoder sandbox still holds %d bytes after caching", used)
	}

	// The pictures of the loop against the decoder's own.
	ctx := context.Background()
	sticker, err := m.vp9.current.rt.OpenSticker(ctx, "reference", data)
	if err != nil {
		t.Fatal(err)
	}
	defer sticker.Close(ctx)
	e := m.entries[msg.Media.ID]
	pictures := m.clipPictures(e, clip)
	for _, i := range []int{0, 7, 29} {
		at := time.Duration(i) * clip.frameDuration
		want, err := sticker.FrameSized(ctx, at, view)
		if err != nil {
			t.Fatal(err)
		}
		im, err := pictures(at)
		if err != nil {
			t.Fatal(err)
		}
		got := im.(*image.RGBA)
		var se float64
		for j := range want.Pix {
			d := float64(want.Pix[j]) - float64(got.Pix[j])
			se += d * d
		}
		if psnr := 10 * math.Log10(255*255/(se/float64(len(want.Pix))+1e-9)); psnr < 40 {
			t.Fatalf("frame %d from planes: PSNR %.1f dB against the decoder's picture", i, psnr)
		}
	}
}
