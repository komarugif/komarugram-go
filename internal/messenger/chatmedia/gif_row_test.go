// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"image"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

func rowGIFs(n int) []model.Message {
	msgs := make([]model.Message, n)
	for i := range msgs {
		id := "gif/" + string(rune('a'+i))
		msgs[i] = model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: id, MIMEType: "image/gif", Width: 120, Height: 80}}
	}
	return msgs
}

// A row of GIFs plays with one FFmpeg, as one picture of its tiles side by
// side, not with one FFmpeg for each GIF; the FFmpeg stops once the row is
// no longer shown.
func TestGIFRowPlaysInOneFFmpeg(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("counts processes in /proc")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	m := NewShared(&fakeSource{data: gifData(t, 24)}, func() {})
	defer m.Close()
	m.ConfigureDecoders(false, false, "")
	msgs := rowGIFs(3)
	widths := []int{60, 90, 45}
	pictures := map[*image.RGBA]bool{}
	most := 0
	for until := time.Now().Add(1500 * time.Millisecond); time.Now().Before(until); {
		m.BeginFrameAt(time.Now())
		im, ok := m.GIFRow(msgs, widths, 50)
		m.EndFrame()
		if !ok {
			t.Fatal("the row is not played as one")
		}
		if im != nil {
			if got := im.Bounds().Size(); got != image.Pt(195, 50) {
				t.Fatalf("the row's picture is %v, want 195x50", got)
			}
			pictures[im] = true
		}
		most = max(most, len(children("ffmpeg")))
		time.Sleep(15 * time.Millisecond)
	}
	if len(pictures) < 10 {
		t.Fatalf("%d pictures of the row in 1.5 s", len(pictures))
	}
	if most != 1 {
		t.Fatalf("%d FFmpeg processes for a row of 3 GIFs", most)
	}
	// Not shown, it stops after gifIdle.
	deadline := time.Now().Add(gifIdle + 3*time.Second)
	for len(children("ffmpeg")) > 0 && time.Now().Before(deadline) {
		m.BeginFrameAt(time.Now())
		m.EndFrame()
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(children("ffmpeg")); n != 0 {
		t.Fatalf("%d FFmpeg processes left after the row was no longer shown", n)
	}
}

// A row that fails, a file FFmpeg cannot read, plays its GIFs one by one
// from then on, each failing or playing alone.
func TestGIFRowFailsToOneByOne(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	m := NewShared(&fakeSource{data: []byte("not a GIF")}, func() {})
	defer m.Close()
	m.ConfigureDecoders(false, false, "")
	msgs := rowGIFs(2)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m.BeginFrameAt(time.Now())
		_, ok := m.GIFRow(msgs, []int{40, 40}, 40)
		m.EndFrame()
		if !ok {
			if len(m.rows) != 0 {
				t.Fatal("the failed row was kept")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("a row of files FFmpeg cannot read still plays as one")
}
