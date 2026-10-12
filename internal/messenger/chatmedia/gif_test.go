// SPDX-License-Identifier: Unlicense

package chatmedia

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/h264"
)

// gifData is an animation of frames moving squares.
func gifData(t *testing.T, frames int) []byte {
	t.Helper()
	g := &gif.GIF{}
	palette := color.Palette{color.RGBA{35, 48, 75, 255}, color.RGBA{130, 190, 250, 255}}
	for n := range frames {
		im := image.NewPaletted(image.Rect(0, 0, 120, 80), palette)
		for y := 20; y < 60; y++ {
			for x := n * 4; x < min(n*4+30, 120); x++ {
				im.SetColorIndex(x, y, 1)
			}
		}
		g.Image = append(g.Image, im)
		g.Delay = append(g.Delay, 4)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// decoders counts the ffmpeg and ffprobe processes this test started.
func decoders(t *testing.T) int {
	t.Helper()
	return len(children("ffmpeg")) + len(children("ffprobe"))
}

// children lists the processes of name this test started.
func children(name string) []string {
	dirs, _ := filepath.Glob("/proc/[0-9]*/stat")
	self := strconv.Itoa(os.Getpid())
	var pids []string
	for _, path := range dirs {
		stat, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s := string(stat)
		open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || end < open {
			continue
		}
		fields := strings.Fields(s[end+1:])
		if len(fields) > 1 && fields[1] == self && s[open+1:end] == name {
			pids = append(pids, s[:strings.IndexByte(s, ' ')])
		}
	}
	return pids
}

// TestGIFStillStartsNoLastingDecoder checks that GIFs shown still keep no
// ffmpeg running, and that one played gives its process back once it stops.
func TestGIFStillStartsNoLastingDecoder(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("counts processes in /proc")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	m := NewShared(&fakeSource{data: gifData(t, 24)}, func() {})
	defer m.Close()
	var msgs []model.Message
	for i := range 8 {
		msgs = append(msgs, model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: fmt.Sprintf("gif/%d", i), MIMEType: "image/gif"}})
	}
	peak := 0
	frame := func(animate map[int]bool) (shown int) {
		m.BeginFrame()
		for i, msg := range msgs {
			status := m.StatusFit(msg, animate[i], image.Pt(100, 100), true)
			if status.Err != nil {
				t.Fatal(status.Err)
			}
			if status.Frame != nil {
				shown++
			}
		}
		m.EndFrame()
		peak = max(peak, decoders(t))
		return shown
	}
	deadline := time.Now().Add(10 * time.Second)
	for frame(nil) < len(msgs) {
		if time.Now().After(deadline) {
			t.Fatal("still frames did not arrive")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if peak > 4 {
		t.Errorf("%d decoders ran at once for still frames", peak)
	}
	for range 20 {
		frame(nil)
		time.Sleep(10 * time.Millisecond)
	}
	if n := decoders(t); n != 0 {
		t.Fatalf("%d decoders stay for still frames", n)
	}
	first := m.entries["gif/0"]
	first.mu.Lock()
	still := first.frame
	first.mu.Unlock()
	moved := false
	for range 200 {
		frame(map[int]bool{0: true})
		first.mu.Lock()
		moved = first.frame != still
		first.mu.Unlock()
		if moved {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !moved {
		t.Fatal("the played GIF did not move")
	}
	if n := decoders(t); n != 1 {
		t.Errorf("%d decoders play one GIF", n)
	}
	// A pass takes about a second; the passes after it keep the process.
	players := map[string]bool{}
	late := map[image.Image]bool{}
	start := time.Now()
	for time.Since(start) < 2500*time.Millisecond {
		frame(map[int]bool{0: true})
		for _, pid := range children("ffmpeg") {
			players[pid] = true
		}
		if time.Since(start) > 1500*time.Millisecond {
			first.mu.Lock()
			late[first.frame] = true
			first.mu.Unlock()
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(players) != 1 {
		t.Errorf("%d ffmpeg processes played one looping GIF", len(players))
	}
	if len(late) < 5 {
		t.Errorf("only %d frames in a later pass", len(late))
	}
	deadline = time.Now().Add(5 * time.Second)
	for decoders(t) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stopped GIF kept its decoder")
		}
		frame(nil)
		time.Sleep(20 * time.Millisecond)
	}
	if status := m.StatusFit(msgs[0], false, image.Pt(100, 100), true); status.Frame != still {
		t.Error("the stopped GIF did not return to its first frame")
	}
}

// gifSource serves a GIF, held until release is closed, and its thumbnail.
type gifSource struct {
	gif, thumb []byte
	release    chan struct{}
}

func (s *gifSource) Media(ctx context.Context, msg model.Message) ([]byte, error) {
	if strings.HasSuffix(msg.Media.ID, "/thumb") {
		return s.thumb, nil
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.gif, nil
}

// TestGIFShowsThumbnailFirst checks that a GIF shows Telegram's thumbnail
// before its file is downloaded, and that one without a thumbnail stops
// loading once downloaded, showing nothing; neither starts ffmpeg.
func TestGIFShowsThumbnailFirst(t *testing.T) {
	var thumb bytes.Buffer
	if err := png.Encode(&thumb, image.NewRGBA(image.Rect(0, 0, 32, 20))); err != nil {
		t.Fatal(err)
	}
	source := &gifSource{gif: gifData(t, 24), thumb: thumb.Bytes(), release: make(chan struct{})}
	m := NewShared(source, func() {})
	defer m.Close()
	with := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif/with", MIMEType: "video/mp4",
		Thumbnail: &model.MessageMedia{ID: "gif/with/thumb", MIMEType: "image/jpeg", Width: 32, Height: 20}}}
	without := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif/without", MIMEType: "video/mp4"}}
	status := func(msg model.Message) Status {
		m.BeginFrame()
		defer m.EndFrame()
		return m.StatusFit(msg, false, image.Pt(100, 100), true)
	}
	deadline := time.Now().Add(5 * time.Second)
	for status(with).Frame == nil {
		if time.Now().After(deadline) {
			t.Fatal("the thumbnail waited for the GIF")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := status(with).Frame.Bounds().Size(); got != image.Pt(32, 20) {
		t.Fatalf("still is %v, not the thumbnail", got)
	}
	if !status(without).Loading {
		t.Fatal("a GIF not downloaded yet is not loading")
	}
	close(source.release)
	deadline = time.Now().Add(5 * time.Second)
	for status(without).Loading {
		if time.Now().After(deadline) {
			t.Fatal("a GIF without a thumbnail kept loading")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s := status(without); s.Frame != nil || s.Err != nil {
		t.Fatalf("a GIF without a thumbnail shows %v, %v", s.Frame, s.Err)
	}
	if runtime.GOOS == "linux" {
		if n := decoders(t); n != 0 {
			t.Fatalf("%d decoders run for GIFs shown still", n)
		}
	}
}

// TestGIFsShareTheFrameClock plays a GIF, with ffmpeg and with the H.264
// decoder in its sandbox, as video avatars play, for a view on the frame's
// clock: once its first picture is shown it wakes the window no more, and the
// frames NextFrame asks for, on the ticks of the clock every animation
// shares, are no more than 30 a second.
func TestGIFsShareTheFrameClock(t *testing.T) {
	cases := []struct {
		name string
		msg  model.Message
		data func(t *testing.T) []byte
		set  func(t *testing.T, m *Manager)
	}{
		{"ffmpeg", model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif", MIMEType: "image/gif"}},
			func(t *testing.T) []byte { return gifData(t, 24) },
			func(t *testing.T, m *Manager) {
				if _, err := exec.LookPath("ffmpeg"); err != nil {
					t.Skip("no ffmpeg")
				}
				m.ConfigureDecoders(false, false, "")
			}},
		{"sandbox", model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "mp4", MIMEType: "video/mp4"}},
			func(t *testing.T) []byte {
				data, err := os.ReadFile("../../../assets/video.mp4")
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			func(t *testing.T, m *Manager) {
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wakes atomic.Int32
			m := NewShared(&fakeSource{data: tc.data(t)}, func() { wakes.Add(1) })
			defer m.Close()
			tc.set(t, m)
			var shown image.Image
			frames, pictures := 0, 0
			draw := func(counting bool) {
				m.BeginFrameAt(time.Now())
				status := m.StatusFit(tc.msg, true, image.Pt(100, 100), true)
				m.EndFrame()
				if status.Err != nil {
					t.Fatal(status.Err)
				}
				if counting {
					frames++
					if status.Frame != shown {
						pictures++
					}
				}
				shown = status.Frame
			}
			// As a window would: a frame when woken, or when one is due.
			run := func(d time.Duration, counting bool) {
				end := time.Now().Add(d)
				last := wakes.Load()
				draw(counting)
				for time.Now().Before(end) {
					next := m.NextFrame()
					if w := wakes.Load(); w != last || !next.IsZero() && !time.Now().Before(next) {
						last = w
						draw(counting)
					}
					time.Sleep(200 * time.Microsecond)
				}
			}
			run(2*time.Second, false)
			before := wakes.Load()
			run(2*time.Second, true)
			woke := wakes.Load() - before
			t.Logf("%d frames, %d pictures a second; woken %d times", frames/2, pictures/2, woke)
			if woke > 0 {
				t.Fatalf("the playing GIF woke the window %d times", woke)
			}
			if frames/2 > 33 || pictures/2 < 10 {
				t.Fatalf("%d frames a second showed %d pictures", frames/2, pictures/2)
			}
		})
	}
}
