// SPDX-License-Identifier: Unlicense

package chatmedia

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/h264"
	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

type fakeSource struct {
	calls atomic.Int32
	data  []byte
	// gate, when set, holds loads until it is closed.
	gate chan struct{}
}

func (s *fakeSource) Media(ctx context.Context, _ model.Message) ([]byte, error) {
	s.calls.Add(1)
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.data, nil
}
func loadedManager(t *testing.T) (*Manager, *fakeSource, model.Message) {
	t.Helper()
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	source := &fakeSource{data: b.Bytes()}
	changed := make(chan struct{}, 8)
	m := New(source, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	t.Cleanup(m.Close)
	msg := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "one", MIMEType: "image/png"}}
	m.BeginFrame()
	m.Frame(msg, false)
	m.EndFrame()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("decode timeout")
	}
	return m, source, msg
}
func TestVisibleMediaSurvivesIdleAndPressure(t *testing.T) {
	m, _, msg := loadedManager(t)
	first, _ := m.Frame(msg, false)
	// An idle window does not advance frame generation. No wall-clock expiry
	// exists: even an arbitrarily delayed next frame keeps the decoded image.
	for i := 0; i < 100; i++ {
		m.BeginFrame()
		frame, _ := m.Frame(msg, false)
		m.EndFrame()
		if frame != first {
			t.Fatal("visible media blinked")
		}
	}
	m.limit = 1
	visible := m.entries["one"]
	m.BeginFrame()
	other := msg
	other.Media = &model.MessageMedia{ID: "two"}
	m.Frame(other, false)
	frame, _ := m.Frame(msg, false)
	m.EndFrame()
	if frame != first || m.entries["one"] != visible {
		t.Fatal("pressure evicted visible entry")
	}
	m.BeginFrame()
	m.EndFrame()
	m.BeginFrame()
	m.EndFrame()
	m.BeginFrame()
	m.Frame(other, false)
	m.EndFrame()
	if _, ok := m.entries["one"]; ok {
		t.Fatal("invisible LRU entry retained under pressure")
	}
}
func TestAnimationStopsOffscreenWithoutDroppingFrame(t *testing.T) {
	m, source, msg := loadedManager(t)
	// The restarted load waits, so that the frame shown meanwhile is the
	// one kept, not one the load decoded again.
	source.gate = make(chan struct{})
	defer close(source.gate)
	frame, _ := m.Frame(msg, true)
	entry := m.entries[msg.Media.ID]
	entry.mu.Lock()
	entry.animated = true
	entry.mu.Unlock()
	m.BeginFrame()
	m.EndFrame()
	m.BeginFrame()
	m.EndFrame()
	entry.mu.Lock()
	expired, still := entry.expired, entry.frame
	entry.mu.Unlock()
	if !expired || still != frame {
		t.Fatal("hidden animation lifecycle")
	}
	m.BeginFrame()
	returned, _ := m.Frame(msg, true)
	m.EndFrame()
	if returned != frame {
		t.Fatal("animation restart flashed placeholder")
	}
}

// A GIF player without a first frame yields a nil *image.RGBA; stored in an
// image.Image it used to pass every nil check and crash Stats.
func TestStatsIgnoresTypedNilFrames(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	t.Cleanup(m.Close)
	var none *image.RGBA
	m.entries["gif"] = &entry{frame: nilImage(none), preview: none, cancel: func() {}}
	if m.entries["gif"].frame != nil {
		t.Fatal("typed nil frame was kept")
	}
	if count, bytes := m.Stats(); count != 1 || bytes != 0 {
		t.Fatalf("Stats = %d, %d", count, bytes)
	}
}

type panicSource struct{}

func (panicSource) Media(context.Context, model.Message) ([]byte, error) { panic("broken source") }

func TestLoadPanicBecomesMediaError(t *testing.T) {
	// os.UserCacheDir reads LOCALAPPDATA on Windows, XDG_CACHE_HOME elsewhere.
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)
	changed := make(chan struct{}, 8)
	m := New(panicSource{}, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	t.Cleanup(m.Close)
	msg := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "bad", MIMEType: "image/png"}}
	m.BeginFrame()
	m.Frame(msg, false)
	m.EndFrame()
	deadline := time.After(time.Second)
	for m.Status(msg, false).Err == nil {
		select {
		case <-changed:
		case <-deadline:
			t.Fatal("panic was not reported as an error")
		}
	}
}

func waitFrame(t *testing.T, m *Manager, msg model.Message, want image.Point, cover bool, changed chan struct{}, size image.Point) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		m.BeginFrame()
		st := m.StatusFit(msg, false, want, cover)
		m.EndFrame()
		if st.Frame != nil && st.Frame.Bounds().Size() == size {
			return
		}
		if st.Err != nil {
			t.Fatal(st.Err)
		}
		select {
		case <-changed:
		case <-deadline:
			got := image.Point{}
			if st.Frame != nil {
				got = st.Frame.Bounds().Size()
			}
			t.Fatalf("want a %v frame for %v, have %v", size, want, got)
		}
	}
}

func TestStillImagesDecodeForTheirBox(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1000, 500)))
	source := &fakeSource{data: b.Bytes()}
	changed := make(chan struct{}, 8)
	m := New(source, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	t.Cleanup(m.Close)
	msg := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "wide", MIMEType: "image/png"}}
	// 200×90 rounds up to 256×128; fitting 1000×500 into it gives 256×128.
	waitFrame(t, m, msg, image.Pt(200, 90), false, changed, image.Pt(256, 128))
	// A smaller tile reuses the frame.
	waitFrame(t, m, msg, image.Pt(100, 40), false, changed, image.Pt(256, 128))
	// Covering a square needs the height, so the image is decoded again.
	waitFrame(t, m, msg, image.Pt(256, 256), true, changed, image.Pt(512, 256))
	// No request means the manager's limit, and never enlarging.
	waitFrame(t, m, msg, image.Pt(4000, 4000), false, changed, image.Pt(1000, 500))
	if n := source.calls.Load(); n != 3 {
		t.Fatalf("source read %d times, want once per decode that grew", n)
	}
}

func TestSharedCacheRetainsScrolledTilesAndPausesAnimations(t *testing.T) {
	m := NewShared(&fakeSource{}, func() {})
	defer m.Close()
	for i := 0; i < 64; i++ {
		key := fmt.Sprint(i)
		m.entries[key] = &entry{frame: image.NewRGBA(image.Rect(0, 0, 16, 16)), box: image.Pt(512, 512), cancel: func() {}}
	}
	for i := 0; i < 64; i++ {
		m.BeginFrame()
		m.StatusFit(model.Message{Media: &model.MessageMedia{ID: fmt.Sprint(i)}}, false, image.Pt(16, 16), false)
		m.EndFrame()
	}
	if len(m.entries) != 64 {
		t.Fatal("scrolled thumbnails discarded", len(m.entries))
	}
	e := &entry{animated: true, animate: true, lastVisible: time.Now(), seen: m.generation, frame: image.NewRGBA(image.Rect(0, 0, 16, 16)), cancel: func() {}}
	m.entries["animation"] = e
	for i := 0; i < 10; i++ {
		m.BeginFrame()
		m.EndFrame()
	}
	if e.animate || e.expired {
		t.Fatal("briefly hidden animation should pause, retaining its decoder")
	}
	e.lastVisible = time.Now().Add(-16 * time.Second)
	m.BeginFrame()
	m.EndFrame()
	if !e.expired || e.frame == nil {
		t.Fatal("expired decoder must keep last frame")
	}
	m.byteLimit = 1024
	m.BeginFrame()
	m.EndFrame()
	_, bytes := m.Stats()
	if bytes > m.byteLimit {
		t.Fatal("offscreen pixels exceed budget", bytes)
	}
}

func TestWebMPreviewUsesRequestedPixelSize(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Fatal(err)
	}
	changed := make(chan struct{}, 16)
	m := NewShared(&fakeSource{data: data}, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer m.Close()
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "webm-preview", MIMEType: "video/webm", Width: 512, Height: 512}}
	deadline := time.After(5 * time.Second)
	for {
		m.BeginFrame()
		status := m.StatusFit(msg, true, image.Pt(56, 56), false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		if status.Frame != nil {
			if got := status.Frame.Bounds().Size(); got != image.Pt(64, 64) {
				t.Fatalf("preview frame size = %v, want 64x64", got)
			}
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatal("preview frame timed out")
		}
	}
}

func TestFourSecondStickerLoopFitsBoundedCache(t *testing.T) {
	sticker := &vp9.Sticker{File: &webm.File{Frames: make([]webm.Frame, 120), FrameDuration: time.Second / 30}, Size: image.Pt(512, 512)}
	if !stickerClipFits(sticker, image.Pt(64, 64)) {
		t.Fatal("a 4-second sticker should fit at grid size")
	}
	if stickerClipFits(sticker, image.Pt(256, 256)) {
		t.Fatal("a full-size loop must stay under the 16 MiB host cache cap")
	}
}

func TestRigbySetFitsGridCacheWhenAvailable(t *testing.T) {
	paths, err := filepath.Glob("../../../assets/rigby/*.webm")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("Rigby sticker files are not present")
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := webm.Demux(data)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		sticker := &vp9.Sticker{File: file, Size: image.Pt(file.Width, file.Height)}
		for _, side := range []int{64, 128} {
			if !stickerClipFits(sticker, image.Pt(side, side)) {
				t.Errorf("%s: %d frames at %v cannot be cached at %dpx", filepath.Base(path), len(file.Frames), file.FrameDuration, side)
			}
		}
	}
}

func TestWebMPreviewCachesLoopAndReusesItAfterScrolling(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeSource{data: data}
	m := NewShared(source, func() {})
	m.ConfigureDecoders(true, false, "")
	defer m.Close()
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "cached-webm", MIMEType: "video/webm", Width: 512, Height: 512}}
	var clip *stickerClip
	started := time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.BeginFrame()
		status := m.StatusFit(msg, true, image.Pt(56, 56), false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		e := m.entries[msg.Media.ID]
		e.mu.Lock()
		clip = e.clip
		e.mu.Unlock()
		if clip != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if clip == nil {
		t.Fatal("video sticker did not finish caching its loop")
	}
	if len(clip.frames) < 2 || clip.bytes > 16<<20 {
		t.Fatalf("cached loop has %d frames and %d bytes", len(clip.frames), clip.bytes)
	}
	t.Logf("cached %d frames in %s using %.2f MiB", len(clip.frames), time.Since(started), float64(clip.bytes)/(1<<20))
	if used := m.vp9.current.rt.Budget().Used(); used != 0 {
		t.Fatalf("decoder sandbox still holds %d bytes after caching", used)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	time.Sleep(220 * time.Millisecond)
	runtime.ReadMemStats(&after)
	if bytes := after.TotalAlloc - before.TotalAlloc; bytes > 2<<20 {
		t.Fatalf("cached playback allocated %.1f MiB in 220ms", float64(bytes)/(1<<20))
	}
	m.BeginFrame()
	m.StatusFit(msg, true, image.Pt(60, 60), false)
	m.EndFrame()
	if m.entries[msg.Media.ID].clip != clip || source.calls.Load() != 1 {
		t.Fatal("small resize restarted a cached sticker")
	}
	m.BeginFrame()
	m.EndFrame()
	m.BeginFrame()
	m.EndFrame()
	if !m.entries[msg.Media.ID].expired {
		t.Fatal("offscreen playback was not stopped")
	}
	m.BeginFrame()
	status := m.StatusFit(msg, true, image.Pt(56, 56), false)
	m.EndFrame()
	if status.Frame == nil || m.entries[msg.Media.ID].clip != clip || source.calls.Load() != 1 {
		t.Fatal("returning to a cached sticker reloaded its source or lost its frame")
	}
}

func TestPausedWebMPreviewDoesNotPredecodeLoop(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Fatal(err)
	}
	m := NewShared(&fakeSource{data: data}, func() {})
	defer m.Close()
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "paused-webm", MIMEType: "video/webm"}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.BeginFrame()
		status := m.StatusFit(msg, false, image.Pt(56, 56), false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		if status.Frame != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.entries[msg.Media.ID].frame == nil {
		t.Fatal("paused sticker did not show its first frame")
	}
	time.Sleep(time.Second)
	e := m.entries[msg.Media.ID]
	e.mu.Lock()
	clip := e.clip
	e.mu.Unlock()
	if clip != nil {
		t.Fatal("paused sticker decoded its whole loop")
	}
}

func TestGIFResizeKeepsItsPlayer(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	msg := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif", MIMEType: "image/gif"}}
	e := &entry{frame: image.NewRGBA(image.Rect(0, 0, 128, 128)), animated: true, box: image.Pt(128, 128), cancel: func() {}}
	m.entries[msg.Media.ID] = e
	m.BeginFrame()
	m.StatusFit(msg, true, image.Pt(256, 256), false)
	m.EndFrame()
	if m.entries[msg.Media.ID] != e {
		t.Fatal("resizing a GIF restarted its independent player")
	}
}

func TestLottiePreviewUsesRequestedPixelSize(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/sample.tgs")
	if err != nil {
		t.Fatal(err)
	}
	m := NewShared(&fakeSource{data: data}, func() {})
	defer m.Close()
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "lottie-preview", MIMEType: "application/x-tgsticker", Width: 512, Height: 512}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.BeginFrame()
		status := m.StatusFit(msg, true, image.Pt(56, 56), false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		if status.Frame != nil {
			if got := status.Frame.Bounds().Size(); got != image.Pt(64, 64) {
				t.Fatalf("preview frame size = %v, want 64x64", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Lottie preview frame timed out")
}

type customEmojiSource struct{ *fakeSource }

func (s customEmojiSource) CustomEmoji(context.Context, int64) (model.Message, error) {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "emoji-document", MIMEType: "application/x-tgsticker"}}, nil
}

func TestCustomEmojiPreviewUsesRequestedPixelSize(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/sample.tgs")
	if err != nil {
		t.Fatal(err)
	}
	m := NewShared(customEmojiSource{&fakeSource{data: data}}, func() {})
	defer m.Close()
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "emoji/1", MIMEType: "application/x-custom-emoji"}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.BeginFrame()
		status := m.StatusFit(msg, true, image.Pt(32, 32), false)
		m.EndFrame()
		if status.Err != nil {
			t.Fatal(status.Err)
		}
		if status.Frame != nil {
			if got := status.Frame.Bounds().Size(); got != image.Pt(32, 32) {
				t.Fatalf("custom emoji frame size = %v, want 32x32", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("custom emoji frame timed out")
}

// A sticker panel or set shows more media at once than the cache keeps off
// screen. Every one of them must load; the limit only bounds what stays
// cached after it leaves the screen.
func TestMediaOnScreenIsNeverRefused(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	m.limit = 2
	shown := make([]model.Message, 5)
	for i := range shown {
		shown[i] = model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: fmt.Sprint(i)}}
	}
	for frame := 0; frame < 4; frame++ {
		m.BeginFrame()
		for _, msg := range shown {
			m.Frame(msg, false)
		}
		m.EndFrame()
	}
	for _, msg := range shown {
		if m.entries[msg.Media.ID] == nil {
			t.Fatalf("media %s on screen never started loading", msg.Media.ID)
		}
	}
	// Once they leave the screen, the cache shrinks back to its limit.
	for frame := 0; frame < 3; frame++ {
		m.BeginFrame()
		m.Frame(shown[0], false)
		m.EndFrame()
	}
	if len(m.entries) > m.limit || m.entries["0"] == nil {
		t.Fatalf("off-screen media kept: %d entries, limit %d", len(m.entries), m.limit)
	}
}

// TestStickerStillsOutliveTheLimit checks that stills of stickers scrolled
// past stay, so that scrolling back shows them without loading them again,
// while photos keep to the manager's limit.
func TestStickerStillsOutliveTheLimit(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 64, 64)))
	source := &fakeSource{data: b.Bytes()}
	m := New(source, func() {})
	defer m.Close()
	show := func(msg model.Message) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			m.BeginFrame()
			status := m.StatusFit(msg, false, image.Pt(64, 64), false)
			m.EndFrame()
			if status.Frame != nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not load", msg.Media.ID)
			}
			time.Sleep(time.Millisecond)
		}
	}
	const stickers = 100
	for i := range stickers {
		show(model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprintf("sticker/%d", i), MIMEType: "image/png"}})
	}
	for i := range 40 {
		show(model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: fmt.Sprintf("photo/%d", i), MIMEType: "image/png"}})
	}
	kept, photos := 0, 0
	for id := range m.entries {
		if strings.HasPrefix(id, "sticker/") {
			kept++
		} else {
			photos++
		}
	}
	if kept != stickers {
		t.Errorf("%d of %d sticker stills kept", kept, stickers)
	}
	if photos > m.limit {
		t.Errorf("%d photos kept over the limit of %d", photos, m.limit)
	}
	calls := source.calls.Load()
	show(model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "sticker/0", MIMEType: "image/png"}})
	if source.calls.Load() != calls {
		t.Error("scrolling back loaded a sticker again")
	}
}

// TestPlayedStickerKeepsItsStillWhenEvicted checks that a sticker played at
// once, with animations on, gives up its decoded loop when scrolled far past
// but keeps its first frame, shown at once when it comes back.
func TestPlayedStickerKeepsItsStillWhenEvicted(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Fatal(err)
	}
	m := New(&fakeSource{data: data}, func() {})
	m.ConfigureDecoders(true, false, "")
	defer m.Close()
	sticker := func(i int) model.Message {
		return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprintf("played/%d", i), MIMEType: "video/webm", Width: 512, Height: 512}}
	}
	count := m.limit + 6
	for i := range count {
		msg := sticker(i)
		deadline := time.Now().Add(5 * time.Second)
		for !m.CheapToPlay(msg, image.Pt(56, 56), false) {
			if time.Now().After(deadline) {
				t.Fatalf("sticker %d did not cache its loop", i)
			}
			m.BeginFrame()
			m.StatusFit(msg, true, image.Pt(56, 56), false)
			m.EndFrame()
			time.Sleep(5 * time.Millisecond)
		}
	}
	e := m.entries[sticker(0).Media.ID]
	if e == nil {
		t.Fatal("the first sticker was dropped with its still")
	}
	e.mu.Lock()
	clip, still := e.clip, e.still
	e.mu.Unlock()
	if clip != nil || still == nil {
		t.Fatalf("evicted sticker keeps loop %v, still %v", clip != nil, still != nil)
	}
	m.BeginFrame()
	status := m.StatusFit(sticker(0), true, image.Pt(56, 56), false)
	m.EndFrame()
	if status.Frame == nil {
		t.Fatal("the sticker scrolled back to shows nothing")
	}
}

// playedStickers plays count video stickers until each has cached its loop.
func playedStickers(t *testing.T, m *Manager, prefix string, count int) []model.Message {
	t.Helper()
	var msgs []model.Message
	for i := range count {
		msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprintf("%s/%d", prefix, i), MIMEType: "video/webm", Width: 512, Height: 512}}
		msgs = append(msgs, msg)
		deadline := time.Now().Add(5 * time.Second)
		for !m.CheapToPlay(msg, image.Pt(56, 56), false) {
			if time.Now().After(deadline) {
				t.Fatalf("%s did not cache its loop", msg.Media.ID)
			}
			m.BeginFrame()
			m.StatusFit(msg, true, image.Pt(56, 56), false)
			m.EndFrame()
			time.Sleep(5 * time.Millisecond)
		}
	}
	return msgs
}

func webmManager(t *testing.T, changed func()) *Manager {
	t.Helper()
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Fatal(err)
	}
	m := New(&fakeSource{data: data}, changed)
	m.ConfigureDecoders(true, false, "")
	t.Cleanup(m.Close)
	return m
}

// TestByteLimitDropsLoopsBeforeStills checks that over the byte limit the
// loops of stickers go first, and their stills stay.
func TestByteLimitDropsLoopsBeforeStills(t *testing.T) {
	m := webmManager(t, func() {})
	m.limit = 100
	msgs := playedStickers(t, m, "loop", 1)
	e := m.entries[msgs[0].Media.ID]
	e.mu.Lock()
	loop := e.clip.bytes
	e.mu.Unlock()
	m.byteLimit = 3 * loop
	msgs = append(msgs, playedStickers(t, m, "more", 9)...)
	m.BeginFrame()
	m.EndFrame()
	for _, msg := range msgs {
		e := m.entries[msg.Media.ID]
		if e == nil || e.still == nil {
			t.Fatalf("%s lost its still, while loops were kept", msg.Media.ID)
		}
	}
}

// TestDropLoopsKeepsStills checks that a closed view's stickers give up
// their loops but keep their stills, while one still drawn keeps its loop.
func TestDropLoopsKeepsStills(t *testing.T) {
	m := webmManager(t, func() {})
	msgs := playedStickers(t, m, "view", 3)
	var freed int64
	m.DropLoops(func(n int64) { freed = n })
	m.BeginFrame()
	m.StatusFit(msgs[2], true, image.Pt(56, 56), false)
	m.EndFrame()
	for i, msg := range msgs {
		e := m.entries[msg.Media.ID]
		if e == nil || e.still == nil {
			t.Fatalf("%s lost its still", msg.Media.ID)
		}
		if drawn := i == 2; (e.clip != nil) != drawn {
			t.Errorf("%s drawn %v keeps its loop %v", msg.Media.ID, drawn, e.clip != nil)
		}
	}
	if freed <= 0 {
		t.Error("the bytes freed were not told")
	}
}

// TestHiddenDecodersAskForFrames checks that decoders left off screen ask
// for the frames that stop them, and nothing more once stopped.
func TestHiddenDecodersAskForFrames(t *testing.T) {
	var asked atomic.Int32
	m := webmManager(t, func() { asked.Add(1) })
	msgs := playedStickers(t, m, "hidden", 1)
	time.Sleep(50 * time.Millisecond)
	asked.Store(0)
	m.BeginFrame()
	m.EndFrame()
	if asked.Load() == 0 {
		t.Fatal("a decoder off screen asked for no frame to stop it")
	}
	m.BeginFrame()
	m.EndFrame()
	e := m.entries[msgs[0].Media.ID]
	if !e.expired {
		t.Fatal("the decoder did not stop")
	}
	time.Sleep(50 * time.Millisecond)
	asked.Store(0)
	m.BeginFrame()
	m.EndFrame()
	if n := asked.Load(); n != 0 {
		t.Fatalf("stopped decoders asked for %d frames", n)
	}
}

// TestPausedAnimationHoldsItsFrame pauses an animation that plays: it stops
// where it is, not on its first frame, and plays again once let.
func TestPausedAnimationHoldsItsFrame(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	still, current := image.NewRGBA(image.Rect(0, 0, 8, 8)), image.NewRGBA(image.Rect(0, 0, 8, 8))
	e := &entry{animated: true, still: still, frame: current, box: image.Pt(1024, 1024), wake: make(chan struct{}, 1), cancel: func() {}}
	m.entries["gif"] = e
	msg := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif"}}
	frame := func() image.Image {
		m.BeginFrame()
		defer m.EndFrame()
		im, _ := m.Frame(msg, true)
		return im
	}
	if frame() != current || !e.animate {
		t.Fatal("the animation does not play")
	}
	m.SetPaused(true)
	if got := frame(); got != current {
		t.Fatal("a paused animation jumped to its first frame")
	}
	if e.animate {
		t.Fatal("a paused animation goes on playing")
	}
	m.SetPaused(false)
	if frame() != current || !e.animate {
		t.Fatal("the animation does not play again")
	}
}

// TestOneFrameLottieDrawsOnce plays a Lottie animation of one frame, as some
// emoji statuses are: it is a picture, published once, and never wakes the
// window again.
func TestOneFrameLottieDrawsOnce(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/sample.json")
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"op":180`), []byte(`"op":1`), 1)
	var changed atomic.Int32
	m := New(&fakeSource{data: data}, func() { changed.Add(1) })
	defer m.Close()
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "status", MIMEType: "application/x-tgsticker"}}
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.BeginFrame()
		frame, err := m.Frame(msg, true)
		m.EndFrame()
		if err != nil {
			t.Fatal(err)
		}
		if frame != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the frame never came")
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := changed.Load()
	for range 30 {
		m.BeginFrame()
		m.Frame(msg, true)
		m.EndFrame()
		time.Sleep(10 * time.Millisecond)
	}
	if woke := changed.Load() - before; woke > 0 {
		t.Fatalf("a still picture woke the window %d times", woke)
	}
}

// TestStatusLoopsPlayOnTheFrameClock plays emoji statuses, Lottie and video
// ones, as the avatars' manager does: decoded once in the pixels they take,
// they wake the window no more, and each frame shows the picture of its own
// time, the next one, of them all at once, scheduled by NextFrame.
func TestStatusLoopsPlayOnTheFrameClock(t *testing.T) {
	for _, tc := range []struct{ file, mime string }{
		{"sample.tgs", "application/x-tgsticker"},
		{"circle.webm", "video/webm"},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			data, err := os.ReadFile("../../../assets/stickers/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			var woke atomic.Int32
			m := NewAvatars(&fakeSource{data: data}, func() { woke.Add(1) })
			defer m.Close()
			var msgs []model.Message
			for i := range 5 {
				msgs = append(msgs, model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprint("status", i), MIMEType: tc.mime}})
			}
			want := image.Pt(16, 16)
			now := time.Unix(1000, 0)
			draw := func() []image.Image {
				m.BeginFrameAt(now)
				defer m.EndFrame()
				var frames []image.Image
				for _, msg := range msgs {
					frames = append(frames, m.StatusFit(msg, true, want, false).Frame)
				}
				return frames
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				draw()
				ready := 0
				for _, msg := range msgs {
					e := m.entries[msg.Media.ID]
					e.mu.Lock()
					if e.clip != nil {
						ready++
					}
					e.mu.Unlock()
				}
				if ready == len(msgs) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%d of %d loops decoded", ready, len(msgs))
				}
				time.Sleep(10 * time.Millisecond)
				now = now.Add(10 * time.Millisecond)
			}
			first := draw()
			if got := first[0].Bounds().Size(); got.X > want.X || got.Y > want.Y {
				t.Fatalf("a status is drawn in %v, on screen in %v", got, want)
			}
			before := woke.Load()
			time.Sleep(300 * time.Millisecond)
			if n := woke.Load() - before; n > 0 {
				t.Fatalf("decoded loops woke the window %d times", n)
			}
			next := m.NextFrame()
			if next.IsZero() || !next.After(now) || next.Sub(now) > time.Second/10 {
				t.Fatalf("next frame at %v, the frame drawn at %v", next, now)
			}
			// Before the next picture of any is due the frame shows the same
			// ones, and once it is due another.
			changed := func(frames []image.Image) int {
				n := 0
				for i := range frames {
					if frames[i] != first[i] {
						n++
					}
				}
				return n
			}
			now = next.Add(-time.Millisecond)
			if n := changed(draw()); n > 0 {
				t.Fatalf("%d pictures changed before they were due", n)
			}
			// The loops started apart, and still change together: on the
			// tick of the clock they share, in one frame.
			now = next
			if n := changed(draw()); n != len(first) {
				t.Fatalf("%d of %d pictures changed in the frame due", n, len(first))
			}
		})
	}
}

// TestAnimationsShareTheFrameClock plays Lottie stickers, drawn by workers,
// for a view on the frame's clock: once their first pictures are shown they
// wake the window no more, and the frames NextFrame asks for, one per tick
// for them all, show 30 pictures a second, or 60 at full rate.
func TestAnimationsShareTheFrameClock(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/sample.tgs")
	if err != nil {
		t.Fatal(err)
	}
	play := func(fullRate bool) (frames, pictures int, woke int32) {
		var wakes atomic.Int32
		m := NewShared(&fakeSource{data: data}, func() { wakes.Add(1) })
		defer m.Close()
		var msgs []model.Message
		for i := range 5 {
			msgs = append(msgs, model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprint("sticker", i), MIMEType: "application/x-tgsticker"}})
		}
		status := m.StatusFit
		if fullRate {
			status = m.StatusFullRate
		}
		var shown image.Image
		draw := func(counting bool) {
			m.BeginFrameAt(time.Now())
			for i, msg := range msgs {
				im := status(msg, true, image.Pt(64, 64), false).Frame
				if i == 0 && counting && im != shown {
					pictures++
				}
				if i == 0 {
					shown = im
				}
			}
			m.EndFrame()
			if counting {
				frames++
			}
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
		run(time.Second, false)
		before := wakes.Load()
		run(2*time.Second, true)
		return frames / 2, pictures / 2, wakes.Load() - before
	}
	frames, pictures, woke := play(false)
	t.Logf("30 a second: %d frames, %d pictures a second", frames, pictures)
	if woke > 0 {
		t.Fatalf("playing animations woke the window %d times", woke)
	}
	if frames > 33 || pictures < 20 {
		t.Fatalf("%d frames a second showed %d pictures, want 30 of both", frames, pictures)
	}
	frames, pictures, _ = play(true)
	t.Logf("full rate: %d frames, %d pictures a second", frames, pictures)
	if frames > 63 || pictures < 36 {
		t.Fatalf("%d frames a second showed %d pictures at full rate, want 60 of both", frames, pictures)
	}
}

// TestPicturesReuseTheirPixels plays animations of every kind a worker
// draws for a view on the frame's clock: Lottie, a VP9 sticker too large to
// keep its loop, and GIFs played by ffmpeg and by the H.264 decoder. Each
// picture is drawn into pixels of a picture no longer shown, not into new
// ones, so that playing makes next to no garbage, and the pictures shown
// share a few pixel buffers.
func TestPicturesReuseTheirPixels(t *testing.T) {
	file := func(name string) func(t *testing.T) []byte {
		return func(t *testing.T) []byte {
			data, err := os.ReadFile("../../../assets/" + name)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	cases := []struct {
		name string
		msg  model.Message
		want image.Point
		data func(t *testing.T) []byte
		set  func(t *testing.T, m *Manager)
		// warming is set for a decoder whose own memory in its sandbox
		// grows over its first few hundred pictures, longer than the test
		// plays: only the pictures' buffers are counted.
		warming bool
	}{
		{"lottie", model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "lottie", MIMEType: "application/x-tgsticker"}}, image.Pt(128, 128),
			func(t *testing.T) []byte {
				// A loop of half a second: the renderer's own memory in its
				// sandbox grows while the animation plays its first loops.
				return bytes.Replace(file("stickers/sample.json")(t), []byte(`"op":180`), []byte(`"op":30`), 1)
			}, nil, false},
		{"vp9", model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "webm", MIMEType: "video/webm"}}, image.Pt(512, 512),
			file("stickers/circle.webm"), nil, false},
		{"ffmpeg", model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif", MIMEType: "image/gif"}}, image.Pt(100, 100),
			func(t *testing.T) []byte { return gifData(t, 24) },
			func(t *testing.T, m *Manager) {
				if _, err := exec.LookPath("ffmpeg"); err != nil {
					t.Skip("no ffmpeg")
				}
				m.ConfigureDecoders(false, false, "")
			}, false},
		{"h264", model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "mp4", MIMEType: "video/mp4"}}, image.Pt(100, 100),
			file("video.mp4"),
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
			}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wakes atomic.Int32
			m := NewShared(&fakeSource{data: tc.data(t)}, func() { wakes.Add(1) })
			defer m.Close()
			if tc.set != nil {
				tc.set(t, m)
			}
			pictures := 0
			var shown image.Image
			var seen []*image.RGBA
			draw := func() {
				m.BeginFrameAt(time.Now())
				im := m.StatusFit(tc.msg, true, tc.want, false).Frame
				m.EndFrame()
				if im != shown {
					pictures++
					shown = im
					if rgba, ok := im.(*image.RGBA); ok && rgba != nil {
						seen = append(seen, rgba)
					}
				}
			}
			// As a window would: a frame when woken, or when one is due.
			run := func(d time.Duration) {
				end := time.Now().Add(d)
				last := wakes.Load()
				draw()
				for time.Now().Before(end) {
					next := m.NextFrame()
					if w := wakes.Load(); w != last || !next.IsZero() && !time.Now().Before(next) {
						last = w
						draw()
					}
					time.Sleep(200 * time.Microsecond)
				}
			}
			run(2 * time.Second)
			pictures, seen = 0, nil
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			run(2 * time.Second)
			runtime.ReadMemStats(&after)
			if pictures < 20 || len(seen) == 0 {
				t.Fatalf("%d pictures in two seconds", pictures)
			}
			size := len(seen[0].Pix)
			perPicture := int(after.TotalAlloc-before.TotalAlloc) / pictures
			t.Logf("%d pictures of %d KB in two seconds, %d KB allocated for each", pictures, size>>10, perPicture>>10)
			if perPicture > size/4 && !tc.warming {
				t.Fatalf("each picture of %d KB made %d KB of garbage", size>>10, perPicture>>10)
			}
			buffers := map[*uint8]bool{}
			for _, im := range seen {
				buffers[&im.Pix[0]] = true
			}
			if len(buffers) > 6 {
				t.Fatalf("%d pictures had %d pixel buffers", len(seen), len(buffers))
			}
		})
	}
}

// Asking for a picture that is loaded allocates nothing: the message was
// moved to the heap on every call, for the loader that only a new entry
// starts, every frame, for each picture shown.
func TestStatusOfLoadedMediaDoesNotAllocate(t *testing.T) {
	m, _, msg := loadedManager(t)
	allocs := testing.AllocsPerRun(100, func() {
		m.BeginFrame()
		m.Status(msg, false)
		m.EndFrame()
	})
	if allocs != 0 {
		t.Fatalf("%v allocations a frame, want none", allocs)
	}
}

type textColorEmojiSource struct{ *fakeSource }

func (s textColorEmojiSource) CustomEmoji(context.Context, int64) (model.Message, error) {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "emoji-document", MIMEType: "application/x-tgsticker", TextColor: true}}, nil
}

// A custom emoji Telegram marks of the text's color is known as one by the
// time its first frame is, for the caller to color; another is not.
func TestCustomEmojiTextColored(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/sample.tgs")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		source Source
		want   bool
	}{{textColorEmojiSource{&fakeSource{data: data}}, true}, {customEmojiSource{&fakeSource{data: data}}, false}} {
		m := NewShared(c.source, func() {})
		msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "emoji/1", MIMEType: "application/x-custom-emoji"}}
		deadline := time.Now().Add(5 * time.Second)
		var frame image.Image
		for frame == nil && time.Now().Before(deadline) {
			m.BeginFrame()
			frame, _ = m.Frame(msg, false)
			m.EndFrame()
			time.Sleep(10 * time.Millisecond)
		}
		if frame == nil || m.TextColored(msg) != c.want {
			t.Fatalf("frame %v, text colored %v, want %v", frame != nil, m.TextColored(msg), c.want)
		}
		m.Close()
	}
}
