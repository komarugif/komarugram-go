// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"context"
	"image"
	"os"
	"os/exec"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/webm"
)

func TestStickerHoverDoesNotAnimateOtherCopies(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	still := image.NewRGBA(image.Rect(0, 0, 16, 16))
	moving := image.NewRGBA(still.Rect)
	e := &entry{frame: moving, still: still, animated: true, box: image.Pt(64, 64), cancel: func() {}}
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "shared", MIMEType: "video/webm"}}
	m.entries["shared"] = e
	m.BeginFrame()
	if got := m.StatusFit(msg, true, image.Pt(32, 32), false).Frame; got != moving {
		t.Fatal("hover did not show animation")
	}
	if got := m.StatusFit(msg, false, image.Pt(32, 32), false).Frame; got != still {
		t.Fatal("hover animated another copy")
	}
	if !e.animate {
		t.Fatal("static copy paused hovered copy")
	}
	m.EndFrame()
	m.BeginFrame()
	m.StatusFit(msg, false, image.Pt(32, 32), false)
	m.EndFrame()
	if e.animate {
		t.Fatal("animation did not stop after hover")
	}
}

// TestStillCopyDrawnFirstDoesNotRestartAnimation draws a still copy of a
// sticker before one that plays, as the profile's gifts lie under the dialog
// of one of them: the animation never stops, which would start it over.
func TestStillCopyDrawnFirstDoesNotRestartAnimation(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	still := image.NewRGBA(image.Rect(0, 0, 16, 16))
	moving := image.NewRGBA(still.Rect)
	e := &entry{frame: moving, still: still, animated: true, box: image.Pt(64, 64), wake: make(chan struct{}, 1), cancel: func() {}}
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "shared", MIMEType: "video/webm"}}
	m.entries["shared"] = e
	for i := 0; i < 3; i++ {
		m.BeginFrame()
		if got := m.StatusFit(msg, false, image.Pt(32, 32), false).Frame; got != still {
			t.Fatal("the still copy animated")
		}
		if i > 0 && !e.animate {
			t.Fatal("the still copy stopped the animation of the other")
		}
		if got := m.StatusFit(msg, true, image.Pt(32, 32), false).Frame; got != moving {
			t.Fatal("the animation does not play")
		}
		m.EndFrame()
		if !e.animate {
			t.Fatal("the animation stopped at the end of the frame")
		}
	}
}

// TestHeldCopyStandsWhereItIs holds a sticker under a dialog: it shows the
// picture it reached, not its first, and is stopped at the end of the frame
// as held.
func TestHeldCopyStandsWhereItIs(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	still := image.NewRGBA(image.Rect(0, 0, 16, 16))
	moving := image.NewRGBA(still.Rect)
	e := &entry{frame: moving, still: still, animated: true, animate: true, box: image.Pt(64, 64), wake: make(chan struct{}, 1), cancel: func() {}}
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "shared", MIMEType: "video/webm"}}
	m.entries["shared"] = e
	m.BeginFrame()
	m.SetHolding(true)
	got := m.StatusFit(msg, true, image.Pt(32, 32), false).Frame
	m.SetHolding(false)
	m.EndFrame()
	if got != moving {
		t.Fatal("a held copy went back to its first picture")
	}
	if e.animate || !e.held {
		t.Fatal("a held copy is not stopped as held")
	}
	m.BeginFrame()
	m.StatusFit(msg, false, image.Pt(32, 32), false)
	m.EndFrame()
	if e.held {
		t.Fatal("a copy stopped without a hold is held")
	}
}

// TestHeldAnimationPlaysOn stops an animation held where it is, and plays
// it again: it goes on from the picture after the one shown, not from its
// first.
func TestHeldAnimationPlaysOn(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const step = 4 * frameTick
	pictures := make(chan int64, 256)
	e := &entry{animate: true, wake: make(chan struct{}, 1), consumed: make(chan struct{}, 1), cancel: func() {}}
	frame := func(at time.Duration) (image.Image, error) {
		pictures <- int64(at / step)
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.playAnimation(ctx, e, frame, step, func(image.Image, error) {})
	}()
	var last int64
	for last < 5 {
		last = <-pictures
	}
	e.mu.Lock()
	e.animate, e.held = false, true
	e.mu.Unlock()
	time.Sleep(3 * step)
	for len(pictures) > 0 {
		last = <-pictures
	}
	e.mu.Lock()
	e.animate = true
	e.mu.Unlock()
	e.wake <- struct{}{}
	if next := <-pictures; next != last+1 {
		t.Fatalf("held at picture %d, it played on from %d", last, next)
	}
	cancel()
	<-done
}

func TestFFmpegStickerAlphaAndHover(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Fatal(err)
	}
	file, err := webm.Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	clip, err := decodeFFmpegSticker(context.Background(), path, data, image.Pt(64, 64), len(file.Frames), file.FrameDuration)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FFmpeg: %d frames in %v, %d bytes", len(clip.frames), time.Since(start), clip.bytes)
	transparent, opaque := false, false
	for _, im := range clip.frames {
		for i := 0; i < len(im.Pix); i += 4 {
			a := im.Pix[i+3]
			transparent = transparent || a == 0
			opaque = opaque || a == 255
			if im.Pix[i] > a || im.Pix[i+1] > a || im.Pix[i+2] > a {
				t.Fatal("pixels are not premultiplied")
			}
		}
	}
	if !transparent || !opaque {
		t.Fatal("lost sticker alpha")
	}
	m := NewShared(&fakeSource{data: data}, func() {})
	defer m.Close()
	e := &entry{ffmpeg: path, box: image.Pt(64, 64)}
	frame, _, ok := m.ffmpegSticker(context.Background(), e, data)
	if !ok {
		t.Fatal("FFmpeg sticker rejected")
	}
	if _, err := frame(time.Second / 3); err != nil {
		t.Fatal(err)
	}
	if e.clip == nil || len(e.clip.frames) != len(file.Frames) {
		t.Fatal("hover failed to decode the loop")
	}
	if m.vp9.current != nil {
		t.Fatal("FFmpeg path started WASM")
	}
}

func TestStickerFFmpegFailureFallsBack(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Fatal(err)
	}
	// The Go test executable is a real executable, but not an FFmpeg decoder.
	bad, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := NewShared(&fakeSource{data: data}, func() {})
	defer m.Close()
	m.ConfigureDecoders(false, false, bad)
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "fallback", MIMEType: "video/webm"}}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m.BeginFrame()
		status := m.StatusFit(msg, true, image.Pt(56, 56), false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		e := m.entries[msg.Media.ID]
		e.mu.Lock()
		complete := e.clip != nil
		e.mu.Unlock()
		if complete {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fallback produced no frame")
}

func TestStickerDecoderChangePreservesCancelledDownloads(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	m.cancelled["photo"] = true
	m.ConfigureDecoders(true, false, "")
	if !m.cancelled["photo"] {
		t.Fatal("changing the sticker decoder restarted a cancelled download")
	}
}
