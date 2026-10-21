// SPDX-License-Identifier: Unlicense

package chatmedia

import (
	"context"
	"image"
	"os"
	"os/exec"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/h264"
)

// TestGIFPausesAndSeeks pauses a GIF that plays, with ffmpeg and with the
// H.264 decoder in its sandbox: it stands on the picture it reached, longer
// than its decoder is kept, and plays on from there; asked to go to a
// time, it plays from there, a paused one too, or, paused, stays paused on
// the picture there, its decoder running or not.
func TestGIFPausesAndSeeks(t *testing.T) {
	data, err := os.ReadFile("../../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	msg := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "mp4", MIMEType: "video/mp4", Duration: 36 * time.Second}}
	for _, tc := range []struct {
		name string
		set  func(t *testing.T, m *Manager)
	}{
		{"ffmpeg", func(t *testing.T, m *Manager) {
			if _, err := exec.LookPath("ffmpeg"); err != nil {
				t.Skip("no ffmpeg")
			}
			m.ConfigureDecoders(false, false, "")
		}},
		{"sandbox", func(t *testing.T, m *Manager) {
			// The module is not in this repository: KOMARUGRAM_AVCDEC
			// names a build of it.
			path := os.Getenv("KOMARUGRAM_AVCDEC")
			if path == "" {
				t.Skip("set KOMARUGRAM_AVCDEC to an avcdec.wasm")
			}
			m.avc.open = func(ctx context.Context) (*h264.Runtime, error) {
				module, err := os.ReadFile(path)
				if err != nil {
					return nil, err
				}
				return h264.NewRuntime(ctx, module)
			}
			m.ConfigureDecoders(false, true, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(&fakeSource{data: data}, func() {})
			defer m.Close()
			tc.set(t, m)
			var shown image.Image
			draw := func() {
				m.BeginFrame()
				status := m.StatusFit(msg, true, image.Pt(100, 100), true)
				m.EndFrame()
				if status.Err != nil {
					t.Fatal(status.Err)
				}
				shown = status.Frame
			}
			// run draws as a window does, until done or for d at most.
			run := func(d time.Duration, done func() bool) {
				for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
					draw()
					if done != nil && done() {
						return
					}
				}
			}
			run(10*time.Second, func() bool { return m.GIFPosition(msg) > 2*time.Second })
			if m.GIFPosition(msg) <= 2*time.Second {
				t.Fatal("the GIF did not play")
			}

			m.ToggleGIF(msg)
			if !m.GIFPaused(msg) {
				t.Fatal("the GIF is not paused")
			}
			run(100*time.Millisecond, nil)
			paused, picture := m.GIFPosition(msg), shown
			// Longer than its decoder stays.
			run(gifIdle*2, nil)
			if at := m.GIFPosition(msg); at != paused || shown != picture {
				t.Fatalf("the paused GIF went from %v to %v, or its picture changed", paused, at)
			}
			m.ToggleGIF(msg)
			// The first picture after is where it stood, or after it.
			run(5*time.Second, func() bool { return m.GIFPosition(msg) != paused })
			if at := m.GIFPosition(msg); at < paused || at > paused+time.Second {
				t.Fatalf("played on from %v, not from %v", at, paused)
			}

			m.SeekGIF(msg, 20*time.Second, true)
			run(5*time.Second, func() bool { return m.GIFPosition(msg) > 20*time.Second })
			if at := m.GIFPosition(msg); at < 19*time.Second || at > 23*time.Second {
				t.Fatalf("plays at %v, not from 20s", at)
			}
			m.ToggleGIF(msg)
			run(100*time.Millisecond, nil)
			m.SeekGIF(msg, 5*time.Second, true)
			if m.GIFPaused(msg) {
				t.Fatal("a paused GIF asked to go to a time stays paused")
			}
			run(5*time.Second, func() bool {
				at := m.GIFPosition(msg)
				return at > 5*time.Second && at < 8*time.Second
			})
			if at := m.GIFPosition(msg); at < 4*time.Second || at > 8*time.Second {
				t.Fatalf("plays at %v, not from 5s", at)
			}

			// Its bar: a paused GIF moved stays paused, on the picture
			// there, while its decoder runs and after it stopped.
			for _, c := range []struct {
				wait time.Duration
				to   time.Duration
			}{{100 * time.Millisecond, 12 * time.Second}, {gifIdle * 2, 30 * time.Second}} {
				m.ToggleGIF(msg)
				run(c.wait, nil)
				before := shown
				m.SeekGIF(msg, c.to, false)
				if !m.GIFPaused(msg) {
					t.Fatal("the bar played the paused GIF")
				}
				run(3*time.Second, func() bool { return shown != before })
				if shown == before {
					t.Fatalf("the paused GIF moved to %v shows the same picture", c.to)
				}
				at, picture := m.GIFPosition(msg), shown
				if at < c.to-time.Second || at > c.to+500*time.Millisecond {
					t.Fatalf("the paused GIF stands at %v, not at %v", at, c.to)
				}
				run(gifIdle*2, nil)
				if m.GIFPosition(msg) != at || shown != picture {
					t.Fatalf("the paused GIF moved to %v went on playing", c.to)
				}
				m.ToggleGIF(msg)
				run(5*time.Second, func() bool { return m.GIFPosition(msg) != at })
				if got := m.GIFPosition(msg); got < at || got > at+time.Second {
					t.Fatalf("played on from %v, not from %v", got, at)
				}
			}
		})
	}
}
