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
	if e.animate {
		t.Fatal("animation did not stop after hover")
	}
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
	frame, ok := m.ffmpegSticker(context.Background(), e, data)
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
