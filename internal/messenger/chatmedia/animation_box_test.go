// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"image"
	"os"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// A custom emoji drawn at a size of its own, as the badge of a name is, is
// decoded in the pixels it is drawn in: a picture of another size is
// resampled, and blurred. Others keep the step, which saves restarting the
// decoder while a window is resized.
func TestSizedAnimatedStickersAreDecodedExactly(t *testing.T) {
	m := New(nil, func() {})
	sized := model.Message{Media: &model.MessageMedia{ID: "emoji/1@22", MIMEType: "application/x-custom-emoji"}}
	for _, want := range []image.Point{{16, 16}, {20, 20}, {22, 22}, {30, 30}, {100, 100}} {
		if got := m.animationBox(sized, want); got != want {
			t.Errorf("%v decoded in %v, want exactly it", want, got)
		}
	}
	plain := model.Message{Media: &model.MessageMedia{ID: "emoji/1", MIMEType: "application/x-custom-emoji"}}
	if got := m.animationBox(plain, image.Pt(22, 22)); got != image.Pt(32, 32) {
		t.Errorf("22 px decoded in %v, want the step's 32", got)
	}
}

// The picture of such a sticker is as large as it is drawn, so that it is
// drawn one pixel to a pixel.
func TestSizedLottieIsRenderedInTheRequestedPixels(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/sample.tgs")
	if err != nil {
		t.Fatal(err)
	}
	m := NewShared(&fakeSource{data: data}, func() {})
	defer m.Close()
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "lottie-sized@22", MIMEType: "application/x-tgsticker", Width: 512, Height: 512}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.BeginFrame()
		status := m.StatusFit(msg, true, image.Pt(22, 22), false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		if status.Frame != nil {
			if got := status.Frame.Bounds().Size(); got != image.Pt(22, 22) {
				t.Fatalf("frame size = %v, want 22x22", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Lottie frame timed out")
}
