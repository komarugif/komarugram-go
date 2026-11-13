// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"image"
	"os"
	"os/exec"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// A GIF the user chose to play with FFmpeg leaves the sandbox; the others
// stay in it, as the settings have them.
func TestPlayGIFWithFFmpeg(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	m.ConfigureDecoders(false, true, "")
	gif := func(id string) model.Message {
		return model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: id, MIMEType: "video/mp4"}}
	}
	chosen, other := gif("gif/chosen"), gif("gif/other")
	m.PlayGIFWithFFmpeg(chosen)
	m.BeginFrame()
	m.StatusFit(chosen, false, image.Pt(100, 100), true)
	m.StatusFit(other, false, image.Pt(100, 100), true)
	m.EndFrame()
	if e := m.entries["gif/chosen"]; e == nil || e.sandboxGIF {
		t.Error("the GIF chosen for FFmpeg plays in the sandbox")
	}
	if e := m.entries["gif/other"]; e == nil || !e.sandboxGIF {
		t.Error("another GIF left the sandbox")
	}
}

// FFmpeg decodes a GIF to the size of its view, as the H.264 sandbox does,
// not to the videos' cap: a list of saved GIFs read and uploaded frames
// several times the size of its tiles, a megabyte a frame.
func TestGIFWithFFmpegFitsItsView(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	// Telegram's GIFs are MP4 files; this one is 720x1280.
	data, err := os.ReadFile("../../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	m := NewShared(&fakeSource{data: data}, func() {})
	defer m.Close()
	m.ConfigureDecoders(false, false, "")
	msg := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif", MIMEType: "video/mp4"}}
	var played int
	var last image.Image
	deadline := time.Now().Add(10 * time.Second)
	for played < 5 && time.Now().Before(deadline) {
		m.BeginFrameAt(time.Now())
		status := m.StatusFit(msg, true, image.Pt(64, 96), false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		if status.Frame != nil && status.Frame != last {
			// The view's size rounded up (Manager.box), 128x128, not
			// 216x384 of the videos' cap.
			box := m.entries["gif"].box
			if got := status.Frame.Bounds().Size(); got.X > box.X || got.Y > box.Y {
				t.Fatalf("a frame of %v for a view of 64x96, decoded for %v", got, box)
			}
			last = status.Frame
			played++
		}
		time.Sleep(20 * time.Millisecond)
	}
	if played == 0 {
		t.Fatal("the GIF did not play")
	}
}

// A GIF asked for by a small view first, as the panel of GIFs asks for the
// one sent from it, is decoded again for a larger view, the message's; and
// the two views, drawn together, do not start it over in turn.
func TestGIFGrowsWithItsView(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	data, err := os.ReadFile("../../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeSource{data: data}
	m := New(source, func() {})
	defer m.Close()
	m.ConfigureDecoders(false, false, "")
	msg := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif", MIMEType: "video/mp4"}}
	tile, message := image.Pt(64, 96), image.Pt(200, 356)
	// play draws frames until done says so, and returns the last picture.
	play := func(both bool, done func(image.Image) bool) image.Image {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			m.BeginFrameAt(time.Now())
			status := m.StatusFit(msg, true, tile, true)
			if both {
				status = m.StatusFit(msg, true, message, false)
			}
			m.EndFrame()
			if status.Err != nil {
				t.Fatal(status.Err)
			}
			if status.Frame != nil && done(status.Frame) {
				return status.Frame
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("the GIF did not show the picture waited for")
		return nil
	}
	small := play(false, func(im image.Image) bool { return true })
	if size := small.Bounds().Size(); size.X > 128 || size.Y > 256 {
		t.Fatalf("a picture of %v for a tile of %v", size, tile)
	}
	large := play(true, func(im image.Image) bool { return im.Bounds().Dx() > small.Bounds().Dx() })
	if size := large.Bounds().Size(); size.X < message.X {
		t.Fatalf("a picture of %v for a message of %v", size, message)
	}
	// Both views drawn for a while: the GIF is read no more.
	loads := source.calls.Load()
	frames := 0
	play(true, func(image.Image) bool { frames++; return frames > 20 })
	if got := source.calls.Load(); got != loads {
		t.Fatalf("the GIF was loaded %d more times with both views drawn", got-loads)
	}
}
