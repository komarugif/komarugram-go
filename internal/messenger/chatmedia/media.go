// SPDX-License-Identifier: Unlicense

// Package chatmedia owns bounded, visible-only media workers. Decrypted
// originals never need a temporary file: ffmpeg and the external player read a private loopback stream.
package chatmedia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/h264"
	"komarugram/pkg/lottie"
	"komarugram/pkg/player"
	"komarugram/pkg/resample"
	"komarugram/pkg/video"
	"komarugram/pkg/vp9"

	_ "golang.org/x/image/webp"
)

type Source interface {
	Media(context.Context, model.Message) ([]byte, error)
}
type entry struct {
	mu     sync.Mutex
	frame  image.Image
	still  image.Image
	wasm   bool
	ffmpeg string
	// sandboxGIF plays the GIF with the H.264 decoder in its sandbox, for
	// want of an FFmpeg.
	sandboxGIF        bool
	wake              chan struct{}
	clip              *stickerClip
	err               error
	seen              uint64
	animated          bool
	caching           bool
	animate           bool
	cancel            context.CancelFunc
	expired           bool
	preview           image.Image
	downloaded, total int64
	lastProgress      time.Time
	lastVisible       time.Time
	cancelled         bool
	// waiting is set once a GIF is ready to play; one with no thumbnail
	// shows nothing until then, and is not loading.
	waiting bool
	// sticker marks a sticker or custom emoji, whose small still is kept
	// beyond the manager's limit.
	sticker bool
	// box is the requested pixel size; it never changes after the entry is
	// created. A larger request replaces the entry.
	box   image.Point
	cover bool
	// clipStart is when the loop played on the UI's clock started, zero
	// while it stands still; clipAt is where in the loop it stopped.
	clipStart time.Time
	clipAt    time.Duration
}
type Manager struct {
	// wasm plays video stickers in the VP9 sandbox, and gifWASM GIFs and
	// animated avatars in the H.264 one, even with an FFmpeg.
	wasm, gifWASM                 bool
	ffmpeg                        string
	retainAnimations              time.Duration
	byteLimit                     int64
	limit, imageSize, videoHeight int
	generation                    uint64
	ctx                           context.Context
	cancel                        context.CancelFunc
	source                        Source
	changed                       func()
	entries                       map[string]*entry // UI thread only
	cancelled                     map[string]bool
	sem                           chan struct{}
	wg                            sync.WaitGroup
	playing                       atomic.Bool
	// dropLoops, when set, drops the loops of stickers off screen at the
	// end of the frame, and is told the bytes freed.
	dropLoops func(freed int64)
	lottie    codec[*lottie.Runtime]
	vp9       codec[*vp9.Runtime]
	avc       codec[*h264.Runtime]
	// sandboxGIFs is set when GIFs play in the H.264 sandbox, one at a
	// time, on hover: when chosen, or when no FFmpeg is found.
	// ffmpegChecked is whether the search was made for the FFmpeg
	// configured.
	sandboxGIFs, ffmpegChecked bool
	// paused stops every animation where it is; UI thread only.
	paused bool
	// uiClips plays decoded loops on the UI's clock: each frame shows the
	// picture of its own time, and the next is scheduled by NextFrame, so
	// that any number of loops wake the window once per picture. Emoji
	// statuses, few, small and often on screen, play so. now is the time of
	// the frame being drawn, next when it is to be drawn again.
	uiClips   bool
	now, next time.Time
}

func New(source Source, changed func()) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{ctx: ctx, cancel: cancel, source: source, changed: changed, limit: 24, imageSize: 1024, videoHeight: 384, byteLimit: 96 << 20, entries: map[string]*entry{}, cancelled: map[string]bool{}, sem: make(chan struct{}, 4)}
	m.lottie.open = lottie.NewRuntime
	m.vp9.open = vp9.NewRuntime
	m.avc.open = newAVCRuntime
	return m
}
func (m *Manager) Frame(msg model.Message, animate bool) (image.Image, error) {
	return m.frame(msg, animate, image.Point{}, false)
}

// decodeStep rounds requested sizes up, so that resizing a window by a few
// pixels does not decode a picture again.
const decodeStep = 128

// box returns the size a still image is decoded for: the request rounded up,
// within the manager's limit. No request means the limit itself.
func (m *Manager) box(want image.Point) image.Point {
	if want.X <= 0 || want.Y <= 0 {
		return image.Pt(m.imageSize, m.imageSize)
	}
	up := func(v int) int { return min(m.imageSize, (v+decodeStep-1)/decodeStep*decodeStep) }
	return image.Pt(up(want.X), up(want.Y))
}

// Animated stickers use a small size step. A 56px grid cell becomes 64px,
// without restarting the decoder for every pixel of a window resize.
func (m *Manager) animationBox(msg model.Message, want image.Point) image.Point {
	const step = 16
	maxSide := min(m.imageSize, 512)
	if msg.Media.MIMEType == "application/x-tgsticker" || msg.Media.MIMEType == "application/x-custom-emoji" {
		maxSide = min(maxSide, 256)
	}
	if want.X <= 0 || want.Y <= 0 {
		return image.Pt(maxSide, maxSide)
	}
	up := func(v int) int { return min(maxSide, (v+step-1)/step*step) }
	return image.Pt(up(want.X), up(want.Y))
}

// CheapToPlay reports whether media shown in want pixels plays without
// starting a costly decoder: a Lottie sticker, which the WASM runtime draws
// in the process, or a video sticker whose loop is decoded already.
func (m *Manager) CheapToPlay(msg model.Message, want image.Point, cover bool) bool {
	if m == nil || msg.Media == nil {
		return false
	}
	if msg.Media.MIMEType == "application/x-tgsticker" || msg.Media.MIMEType == "application/x-custom-emoji" {
		return true
	}
	e := m.entries[msg.Media.ID]
	if e == nil {
		return false
	}
	box := m.animationBox(msg, want)
	e.mu.Lock()
	defer e.mu.Unlock()
	// A larger view drops the loop, as frame does.
	return e.clip != nil && box.X <= e.box.X && box.Y <= e.box.Y && (!cover || e.cover)
}

func (m *Manager) frame(msg model.Message, animate bool, want image.Point, cover bool) (image.Image, error) {
	if msg.Media == nil {
		return nil, nil
	}
	key := msg.Media.ID
	box := m.box(want)
	animatedSticker := msg.Media.MIMEType == "application/x-tgsticker" || msg.Media.MIMEType == "application/x-custom-emoji" || msg.Kind == model.MessageSticker && msg.Media.MIMEType == "video/webm"
	if animatedSticker {
		box = m.animationBox(msg, want)
	}
	e := m.entries[key]
	var restart bool
	var grown bool
	if e != nil {
		e.mu.Lock()
		// A smaller decoded image is replaced when the view grows; the old
		// frame stays on screen meanwhile.
		grown = (!e.animated || animatedSticker) && (box.X > e.box.X || box.Y > e.box.Y || cover && !e.cover)
		restart = e.expired || grown && (e.frame != nil || e.err != nil)
		e.mu.Unlock()
	}
	if e == nil || restart {
		var previous, preview, still image.Image
		var clip *stickerClip
		wasm := m.wasm
		if restart {
			e.cancel()
			e.mu.Lock()
			previous, preview, still = e.frame, e.preview, e.still
			wasm = wasm || e.wasm
			if !grown {
				clip = e.clip
			} else {
				still = nil
			}
			e.mu.Unlock()
		}
		if e == nil && m.heavyEntries() >= m.limit {
			// The limit bounds what stays cached off screen, not what is
			// on screen: a sticker panel shows more than it keeps. When
			// everything was drawn a frame ago, the cache grows, and
			// EndFrame shrinks it once media leaves the screen.
			m.evict(true)
		}
		ctx, cancel := context.WithCancel(m.ctx)
		e = &entry{wake: make(chan struct{}, 1), wasm: wasm, ffmpeg: m.ffmpeg, sandboxGIF: m.SandboxGIFs(), seen: m.generation, animate: animate, cancel: cancel, cancelled: m.cancelled[key], frame: previous, preview: preview, still: still, clip: clip, box: box, cover: cover, sticker: animatedSticker || msg.Kind == model.MessageSticker}
		m.entries[key] = e
		m.wg.Go(func() {
			defer crash.Recover("chatmedia load "+key, func(p *crash.Panic) {
				e.mu.Lock()
				e.err = p
				e.mu.Unlock()
				m.changed()
			})
			m.load(ctx, e, msg)
		})
	}
	if m.paused {
		animate = false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	wasAnimating := e.animate
	e.animate = animate || e.seen == m.generation && e.animate
	if e.animate && !wasAnimating {
		select {
		case e.wake <- struct{}{}:
		default:
		}
	}
	e.seen = m.generation
	e.lastVisible = time.Now()
	if m.uiClips && e.clip != nil {
		if im := m.clipFrame(e); im != nil {
			return im, e.err
		}
	}
	// A paused animation stays on the frame it reached.
	if !animate && e.still != nil && (!m.paused || e.frame == nil) {
		return e.still, e.err
	}
	return e.frame, e.err
}

// SetPaused stops every animation where it is, or lets them play again, as
// Telegram Desktop pauses them while its window is not active. It runs on
// the UI goroutine.
func (m *Manager) SetPaused(paused bool) { m.paused = paused }

// clipFrame is the picture of e's loop at the time of the frame, nil while
// it stands still, and schedules the next. The caller holds e.mu.
func (m *Manager) clipFrame(e *entry) image.Image {
	if !e.animate {
		e.clipStart = time.Time{}
		return nil
	}
	// Loops change on the ticks of the clock every animation shares, so
	// that those on screen change together, in one frame, however often
	// the window draws.
	now := m.now.Truncate(animationTick)
	if e.clipStart.IsZero() {
		// A loop that stood still goes on from where it stopped.
		e.clipStart = now.Add(-e.clipAt)
	}
	step := e.clip.frameDuration
	elapsed := now.Sub(e.clipStart)
	index := int(elapsed / step)
	e.clipAt = elapsed % (step * time.Duration(len(e.clip.frames)))
	e.frame = e.clip.frames[index%len(e.clip.frames)]
	// The next picture shows on the tick after it is due.
	next := e.clipStart.Add(step * time.Duration(index+1))
	if late := next.Sub(next.Truncate(animationTick)); late > 0 {
		next = next.Add(animationTick - late)
	}
	if m.next.IsZero() || next.Before(m.next) {
		m.next = next
	}
	return e.frame
}

// Visibility is tied to completed UI frames, not elapsed time. An idle window
// may go minutes without a frame while every image is still on screen.
func (m *Manager) BeginFrame() { m.BeginFrameAt(time.Now()) }

// BeginFrameAt begins a frame drawn for now, the time loops played on the
// UI's clock show.
func (m *Manager) BeginFrameAt(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	m.generation++
	m.now, m.next = now, time.Time{}
}

// NextFrame is when a loop drawn in the frame shows its next picture, zero
// for never: the window is to draw a frame again then. It runs on the UI
// goroutine, after the frame's media were asked for.
func (m *Manager) NextFrame() time.Time { return m.next }

// ConfigureDecoders runs on the UI goroutine. New workers capture these settings.
// wasm and gifWASM choose the sandboxes for video stickers and for GIFs over
// an FFmpeg.
func (m *Manager) ConfigureDecoders(wasm, gifWASM bool, ffmpeg string) {
	if m.wasm == wasm && m.gifWASM == gifWASM && m.ffmpeg == ffmpeg {
		return
	}
	m.wasm, m.gifWASM, m.ffmpeg = wasm, gifWASM, ffmpeg
	m.ffmpegChecked = false
	m.Release()
}

// SandboxGIFs reports whether GIFs play in the H.264 sandbox, chosen or for
// want of an FFmpeg. It is several times as costly: a GIF then plays only
// while the pointer is on it. The search for FFmpeg is made once per
// configuration.
func (m *Manager) SandboxGIFs() bool {
	if !m.ffmpegChecked {
		m.sandboxGIFs = m.gifWASM || video.ResolveFFmpeg(m.ffmpeg) == ""
		m.ffmpegChecked = true
	}
	return m.sandboxGIFs
}
func (m *Manager) EndFrame() {
	defer m.trimBytes()
	defer m.trimAnimations()
	// stopping counts decoders off screen that the next frames stop.
	stopping := 0
	for _, e := range m.entries {
		e.mu.Lock()
		if e.seen != m.generation {
			e.animate = false
		}
		stops := e.caching || e.clip != nil || m.retainAnimations == 0
		if e.animated && !e.expired && e.seen+1 < m.generation && (stops || time.Since(e.lastVisible) > m.retainAnimations) {
			e.cancel()
			e.expired = true
		}
		if e.animated && !e.expired && e.seen != m.generation && stops {
			stopping++
		}
		e.mu.Unlock()
	}
	if done := m.dropLoops; done != nil {
		m.dropLoops = nil
		var freed int64
		for key, e := range m.entries {
			e.mu.Lock()
			drop := e.sticker && e.clip != nil && e.seen != m.generation
			e.mu.Unlock()
			if drop {
				freed += m.shrink(key)
			}
		}
		if freed > 0 {
			done(freed)
		}
	}
	for m.heavyEntries() > m.limit && m.evict(true) {
	}
	for len(m.entries) > m.limit+lightStickers && m.evict(false) {
	}
	// A view closed, nothing may draw again for a while: the decoders it
	// left would keep running, and their memory, until some input.
	if stopping > 0 {
		m.changed()
	}
}

// DropLoops drops, at the end of the frame, the decoded loops of stickers
// not drawn in it, keeping their stills: a view of stickers closed, and
// shown again it shows their first frames at once. done is told the bytes
// freed, if any.
func (m *Manager) DropLoops(done func(freed int64)) {
	m.dropLoops = done
}

// Forget drops the media of ids, stills included: a view of them is not
// likely to be shown again.
func (m *Manager) Forget(ids []string) {
	for _, id := range ids {
		if e := m.entries[id]; e != nil {
			e.cancel()
			delete(m.entries, id)
		}
	}
}

// A sticker whose pixels take at most lightStickerBytes, as a still of a
// grid cell does, is not counted in the manager's limit: scrolling a sticker
// set back shows it at once instead of loading it again. Up to lightStickers
// of them are kept, within the byte limit.
const (
	lightStickerBytes = 128 << 10
	lightStickers     = 256
)

// heavyEntries counts the entries within the manager's limit.
func (m *Manager) heavyEntries() int {
	n := 0
	for _, e := range m.entries {
		e.mu.Lock()
		if !e.light() {
			n++
		}
		e.mu.Unlock()
	}
	return n
}

// light reports whether e is outside the manager's limit. The caller holds
// e.mu.
func (e *entry) light() bool {
	return e.sticker && e.bytes() <= lightStickerBytes
}

// evict drops the entry drawn longest ago, if not in the last two frames;
// heavy leaves light entries alone. A sticker that played keeps its still
// as a light entry, and gives up its loop and current frame: scrolled back
// to, it shows its first frame at once instead of its placeholder.
func (m *Manager) evict(heavy bool) bool {
	var key string
	oldest := m.generation
	for k, e := range m.entries {
		e.mu.Lock()
		seen, light := e.seen, e.light()
		e.mu.Unlock()
		if seen < oldest && !(heavy && light) {
			key, oldest = k, seen
		}
	}
	// Keep both the current and previous frame while a list is being traversed.
	if key == "" || oldest+1 >= m.generation {
		return false
	}
	m.shrink(key)
	return true
}

// shrink stops the entry of key and drops it, or, for a sticker that played,
// drops all but its still, making it light. It returns the bytes freed.
func (m *Manager) shrink(key string) (freed int64) {
	e := m.entries[key]
	e.cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	before := e.bytes()
	keep := e.sticker && e.still != nil && (e.clip != nil || e.frame != e.still) &&
		imageBytes(e.still)+imageBytes(e.preview) <= lightStickerBytes
	if !keep {
		delete(m.entries, key)
		return before
	}
	// Stopped, it is loaded again when asked for, from its still.
	e.clip, e.frame, e.expired = nil, e.still, true
	return before - e.bytes()
}
func (m *Manager) Retry(msg model.Message) {
	if msg.Media != nil {
		delete(m.cancelled, msg.Media.ID)
		if e := m.entries[msg.Media.ID]; e != nil {
			e.cancel()
			delete(m.entries, msg.Media.ID)
		}
	}
}

// Retain drops every entry but those of the given media IDs, so that a
// view which knows what it needs next does not wait for older entries to age
// out. It runs on the owning UI goroutine.
func (m *Manager) Retain(ids ...string) {
	for key, e := range m.entries {
		keep := false
		for _, id := range ids {
			keep = keep || key == id
		}
		if !keep {
			e.cancel()
			delete(m.entries, key)
		}
	}
}

// Clear cancels every load and drops every frame, so that a closed view
// holds no pixels. It runs on the owning UI goroutine; the manager stays usable.
func (m *Manager) Clear() {
	for key, e := range m.entries {
		e.cancel()
		delete(m.entries, key)
	}
	clear(m.cancelled)
}

// Release is Clear for a hidden window: it drops every frame but remembers
// which downloads the user cancelled, and closes the decoder runtimes once
// their workers have stopped. It runs on the owning UI goroutine; frames are
// loaded again, from the media cache, when they are asked for.
func (m *Manager) Release() {
	for key, e := range m.entries {
		e.cancel()
		delete(m.entries, key)
	}
	m.lottie.retire()
	m.vp9.retire()
	m.avc.retire()
}

func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
	m.lottie.retire()
	m.vp9.retire()
	m.avc.retire()
}
func (m *Manager) load(ctx context.Context, e *entry, msg model.Message) {
	publish := func(im image.Image, err error) {
		if ctx.Err() != nil {
			return
		}
		im = nilImage(im)
		e.mu.Lock()
		changed := im != e.frame || err != nil
		if im != nil {
			if e.still == nil {
				e.still = im
			}
			e.frame = im
		}
		e.err = err
		e.mu.Unlock()
		if changed {
			m.changed()
		}
	}
	if e.clip != nil && m.uiClips {
		return // The view plays the loop.
	}
	if e.clip != nil {
		m.playAnimation(ctx, e, func(t time.Duration) (image.Image, error) {
			return e.clip.FrameAt(t), nil
		}, publish)
		return
	}
	if im := previewImage(msg.Media.Preview); im != nil {
		e.mu.Lock()
		e.preview = im
		e.mu.Unlock()
		m.changed()
	}
	e.mu.Lock()
	cancelled := e.cancelled
	e.mu.Unlock()
	if cancelled {
		return
	}
	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	// Released once, early on the normal paths and by the defer on a panic.
	release := sync.OnceFunc(func() { <-m.sem })
	defer release()
	var err error
	switch msg.Media.MIMEType {
	case "application/x-avatar-video":
		if source, ok := m.source.(interface {
			AvatarVideo(context.Context, string) (model.Message, error)
		}); ok {
			msg, err = source.AvatarVideo(ctx, msg.Media.ID)
		} else {
			err = errors.New("avatar video unavailable")
		}
	case "application/x-custom-emoji":
		if source, ok := m.source.(interface {
			CustomEmoji(context.Context, int64) (model.Message, error)
		}); ok {
			var id int64
			fmt.Sscanf(msg.Media.ID, "emoji/%d", &id)
			msg, err = source.CustomEmoji(ctx, id)
		} else {
			err = errors.New("custom emoji unavailable")
		}
	}
	if err == nil && msg.Kind == model.MessageGIF {
		e.mu.Lock()
		still := e.still
		e.mu.Unlock()
		if still == nil {
			publish(m.gifThumbnail(ctx, msg), nil)
		}
	}
	var data []byte
	if err == nil {
		if source, ok := m.source.(interface {
			MediaProgress(context.Context, model.Message, func(int64, int64)) ([]byte, error)
		}); ok {
			data, err = source.MediaProgress(ctx, msg, func(done, total int64) {
				e.mu.Lock()
				e.downloaded, e.total = done, total
				notify := time.Since(e.lastProgress) > 100*time.Millisecond
				if notify {
					e.lastProgress = time.Now()
				}
				e.mu.Unlock()
				if notify {
					m.changed()
				}
			})
		} else {
			data, err = m.source.Media(ctx, msg)
		}
	}
	if err != nil {
		release()
		publish(nil, err)
		return
	}
	var frame func(time.Duration) (image.Image, error)
	var close func()
	switch {
	case msg.Media.MIMEType == "application/x-tgsticker":
		var rt *lottie.Runtime
		var done func()
		rt, done, err = m.lottie.acquire(ctx)
		// Released after the animation, whose close is deferred later.
		defer done()
		if err == nil {
			var a *lottie.Animation
			a, err = rt.Open(ctx, msg.Media.ID, data, min(256, max(e.box.X, e.box.Y)))
			if err == nil && a.FrameCount <= 1 {
				// One frame is a picture, as some emoji statuses are: it is
				// drawn once, and no ticker draws it again for nothing.
				im, _, er := a.FrameAt(ctx, 0)
				a.Close(context.Background())
				release()
				if er != nil {
					publish(nil, er)
					return
				}
				publish(clone(im), nil)
				return
			}
			if err == nil && m.uiClips && lottieClipFits(a) {
				close = func() { a.Close(context.Background()) }
				err = m.decodeLottieLoop(ctx, e, a, release, publish)
			} else if err == nil {
				frame = func(t time.Duration) (image.Image, error) { im, _, er := a.FrameAt(ctx, t); return clone(im), er }
				close = func() { a.Close(context.Background()) }
			}
		}
	case msg.Kind == model.MessageSticker && msg.Media.MIMEType == "video/webm":
		// Static previews never start an external process or retain decoder
		// instances. The compiled WASM runtime is shared across stickers.
		e.mu.Lock()
		e.animated = true
		preview := e.still
		e.mu.Unlock()
		if preview == nil {
			preview, err = m.stickerPreview(ctx, msg.Media.ID, data, e.box)
			if err != nil {
				break
			}
		}
		publish(preview, nil)
		release()
		// Dormant stickers sleep until hover/autoplay, without a frame ticker
		// or a semaphore slot held while another sticker needs decoding.
		release, err = m.waitStickerPlayback(ctx, e)
		if err != nil {
			return
		}
		defer release()
		if !e.wasm {
			if f, ok := m.ffmpegSticker(ctx, e, data); ok {
				frame = f
				break
			}
		}
		var rt *vp9.Runtime
		var done func()
		rt, done, err = m.vp9.acquire(ctx)
		defer done()
		if err == nil {
			var a *vp9.Sticker
			a, err = rt.OpenSticker(ctx, msg.Media.ID, data)
			if err == nil {
				e.mu.Lock()
				animate := e.animate
				e.mu.Unlock()
				if animate && stickerClipFits(a, e.box) {
					e.mu.Lock()
					e.animated, e.caching = true, true
					e.mu.Unlock()
					var clip *stickerClip
					clip, err = decodeStickerClip(ctx, a, e.box, func(im *image.RGBA) { publish(im, nil) })
					a.Close(context.Background())
					done()
					e.mu.Lock()
					e.caching = false
					if err == nil && ctx.Err() == nil {
						e.clip = clip
					}
					e.mu.Unlock()
					if err == nil && clip != nil {
						frame = func(t time.Duration) (image.Image, error) { return clip.FrameAt(t), nil }
					}
				} else {
					var lastIndex = -1
					var last image.Image
					frame = func(t time.Duration) (image.Image, error) {
						index := int(t/a.File.FrameDuration) % a.FrameCount()
						if index == lastIndex {
							return last, nil
						}
						im, er := stickerFrame(ctx, a, t, e.box)
						if er != nil {
							return nil, er
						}
						last = im
						lastIndex = index
						return last, nil
					}
					close = func() { a.Close(context.Background()) }
				}
			}
		}
	case msg.Kind == model.MessageGIF:
		// Like a video sticker, a GIF shown still keeps no decoder. It shows
		// Telegram's thumbnail, published before the download, as Telegram
		// Desktop does; Telegram gives large GIFs none, and they show nothing
		// until played. An ffmpeg plays a GIF only on request.
		e.mu.Lock()
		e.animated = true
		still := e.still
		e.mu.Unlock()
		if still == nil && msg.Media.MIMEType == "image/gif" {
			// A real GIF file has its first frame decoded in the process.
			if im, er := gif.Decode(bytes.NewReader(data)); er == nil {
				publish(toRGBA(im), nil)
			}
		}
		if e.sandboxGIF {
			e.mu.Lock()
			e.waiting = true
			e.mu.Unlock()
			m.changed()
			release()
			if msg.Media.MIMEType != "image/gif" {
				m.playGIFSandbox(ctx, e, data, publish)
			}
			return
		}
		var stream *player.Stream
		stream, err = player.Serve("animation", bytes.NewReader(data), int64(len(data)))
		if err != nil {
			break
		}
		defer stream.Close()
		e.mu.Lock()
		e.waiting = true
		e.mu.Unlock()
		m.changed()
		release()
		m.playGIF(ctx, e, stream.URL(), publish)
		return
	default:
		var cfg image.Config
		cfg, _, err = image.DecodeConfig(bytes.NewReader(data))
		if err == nil && (cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 24_000_000) {
			err = errors.New("image dimensions exceed limit")
		}
		if err == nil {
			var im image.Image
			im, _, err = image.Decode(bytes.NewReader(data))
			// Scale and normalize decoded JPEG/WebP off the UI thread. Gio can
			// then upload an immutable RGBA frame without copying pixels in Layout.
			if err == nil && im != nil {
				bounds := im.Bounds()
				size := resample.Fit(bounds.Dx(), bounds.Dy(), e.box, e.cover)
				// Covering a narrow box can ask for a long side past the limit.
				size = resample.Fit(size.X, size.Y, image.Pt(m.imageSize, m.imageSize), false)
				if size != bounds.Size() {
					im = resample.Resize(im, size.X, size.Y)
				} else if _, ok := im.(*image.RGBA); !ok {
					rgba := image.NewRGBA(image.Rectangle{Max: size})
					draw.Draw(rgba, rgba.Bounds(), im, bounds.Min, draw.Src)
					im = rgba
				}
			}
			publish(im, err)
			release()
			return
		}
	}
	release()
	if err != nil {
		publish(nil, err)
		return
	}
	if close != nil {
		defer close()
	}
	if m.uiClips {
		e.mu.Lock()
		clip := e.clip
		e.mu.Unlock()
		if clip != nil {
			// The view plays the loop from here on.
			m.changed()
			return
		}
	}
	if frame == nil {
		return
	}
	m.playAnimation(ctx, e, frame, publish)
}

// gifThumbnail is Telegram's thumbnail of a GIF, decoded in the process, or
// nil when there is none.
func (m *Manager) gifThumbnail(ctx context.Context, msg model.Message) image.Image {
	thumb := msg.Media.Thumbnail
	if thumb == nil || thumb.Width <= 0 || thumb.Height <= 0 {
		return nil
	}
	data, err := m.source.Media(ctx, model.Message{Kind: model.MessagePhoto, Media: thumb})
	if err != nil {
		return nil
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return toRGBA(im)
}

// toRGBA copies im into an RGBA image at the origin.
func toRGBA(im image.Image) *image.RGBA {
	rgba := image.NewRGBA(image.Rectangle{Max: im.Bounds().Size()})
	draw.Draw(rgba, rgba.Rect, im, im.Bounds().Min, draw.Src)
	return rgba
}

// gifIdle is how long a GIF that stopped playing keeps its ffmpeg, so that
// the pointer passing over it again, or a short scroll, does not restart it.
const gifIdle = time.Second

// playGIF plays a GIF from url whenever it is to animate, with an ffmpeg
// that it stops once the GIF has not animated for gifIdle. The file is
// probed for the size of its frames once, when it first plays.
func (m *Manager) playGIF(ctx context.Context, e *entry, url string, publish func(image.Image, error)) {
	var size image.Point
	for {
		if err := waitAnimation(ctx, e); err != nil {
			return
		}
		var p *video.Player
		if size == (image.Point{}) {
			var err error
			p, err = video.NewPlayerWithFFmpeg(url, m.videoHeight, e.ffmpeg)
			if err != nil {
				publish(nil, err)
				return
			}
			size = image.Pt(p.Info().Width, p.Info().Height)
		} else {
			p = video.NewPlayerOfSize(url, size, e.ffmpeg)
		}
		p.Start()
		err := playGIFFrames(ctx, e, p, publish)
		p.Stop()
		if err != nil {
			publish(nil, err)
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// playGIFSandbox plays an MP4 GIF with the H.264 decoder whenever it is to
// animate, with a decoder instance it closes once the GIF has not animated
// for gifIdle, as playGIF does with FFmpeg.
func (m *Manager) playGIFSandbox(ctx context.Context, e *entry, data []byte, publish func(image.Image, error)) {
	for {
		if err := waitAnimation(ctx, e); err != nil {
			return
		}
		rt, done, err := m.avc.acquire(ctx)
		if err != nil {
			publish(nil, err)
			return
		}
		v, err := rt.OpenVideo(ctx, data)
		if err != nil {
			done()
			publish(nil, err)
			return
		}
		err = playVideoFrames(ctx, e, v, publish)
		v.Close(context.Background())
		done()
		if err != nil {
			publish(nil, err)
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// playVideoFrames publishes the pictures of v, each at its time, while the
// GIF animates, and returns once it has not for gifIdle.
func playVideoFrames(ctx context.Context, e *entry, v *h264.Video, publish func(image.Image, error)) error {
	size := resample.Fit(v.Size().X, v.Size().Y, e.box, e.cover)
	var idle, due, start time.Time
	for {
		e.mu.Lock()
		animate := e.animate
		e.mu.Unlock()
		wait := time.Second / 30
		if animate {
			idle = time.Time{}
			i, err := v.Next(ctx)
			if err != nil {
				return err
			}
			publish(v.Picture(size), nil)
			now := time.Now()
			if i == 0 || due.IsZero() {
				// A loop, or a pause, starts the clock again.
				start = now.Add(-v.Time(i))
			}
			// Behind time, the next picture follows at once: a slow machine
			// plays slower rather than skipping, which H.264 cannot.
			due = start.Add(v.Time(i + 1))
			wait = max(0, due.Sub(now))
		} else {
			due = time.Time{}
			if idle.IsZero() {
				idle = time.Now()
			} else if time.Since(idle) > gifIdle {
				return nil
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		case <-e.wake:
			// Animation asked for again: the paused GIF resumes at once.
			timer.Stop()
		}
	}
}

// playGIFFrames publishes the frames of p while the GIF animates, and
// returns once it has not for gifIdle.
func playGIFFrames(ctx context.Context, e *entry, p *video.Player, publish func(image.Image, error)) error {
	tick := time.NewTicker(time.Second / 30)
	defer tick.Stop()
	var idle time.Time
	for {
		e.mu.Lock()
		animate := e.animate
		e.mu.Unlock()
		p.SetPaused(!animate)
		if animate {
			idle = time.Time{}
			im, err := p.Frame()
			if err != nil {
				return err
			}
			if im != nil {
				publish(im, nil)
			}
		} else if idle.IsZero() {
			idle = time.Now()
		} else if time.Since(idle) > gifIdle {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// waitAnimation blocks until e is to animate.
func waitAnimation(ctx context.Context, e *entry) error {
	for {
		e.mu.Lock()
		animate := e.animate
		e.mu.Unlock()
		if animate {
			return nil
		}
		select {
		case <-e.wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *Manager) playAnimation(ctx context.Context, e *entry, frame func(time.Duration) (image.Image, error), publish func(image.Image, error)) {
	e.mu.Lock()
	e.animated = true
	e.mu.Unlock()
	started := time.Now()
	first := true
	wasAnimating := false
	for {
		e.mu.Lock()
		animate := e.animate
		e.mu.Unlock()
		if animate && !wasAnimating {
			started = time.Now()
		}
		wasAnimating = animate
		if first || animate {
			elapsed := time.Since(started)
			if !animate {
				elapsed = 0
			}
			im, err := frame(elapsed)
			if err != nil {
				publish(nil, err)
				return
			}
			if im != nil && (first || animate) {
				publish(im, nil)
				first = false
			}
		}
		// Every animation ticks on the same clock, so that those on screen
		// together publish together and wake the window once, not each in
		// turn.
		timer := time.NewTimer(time.Until(time.Now().Truncate(animationTick).Add(animationTick)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// animationTick is how often an animation played by a worker shows its
// next picture.
const animationTick = time.Second / 30

// nilImage turns a typed nil, such as a nil *image.RGBA stored in an
// image.Image, into a plain nil so that "im != nil" means there are pixels.
func nilImage(im image.Image) image.Image {
	switch v := im.(type) {
	case *image.RGBA:
		if v == nil {
			return nil
		}
	case *image.NRGBA:
		if v == nil {
			return nil
		}
	case *image.YCbCr:
		if v == nil {
			return nil
		}
	}
	return im
}

func clone(im *image.RGBA) image.Image {
	if im == nil {
		return nil
	}
	return cloneRGBA(im)
}

// cloneRGBA copies im, which the renderer reuses, into an image of its own.
func cloneRGBA(im *image.RGBA) *image.RGBA {
	out := image.NewRGBA(im.Bounds())
	draw.Draw(out, out.Bounds(), im, im.Bounds().Min, draw.Src)
	return out
}

// Play opens a single external player of kind, the one at path or the one
// found when path is "", and owns its in-memory stream until exit.
func (m *Manager) Play(msg model.Message, kind player.Kind, path string, report func(error)) {
	if !m.playing.CompareAndSwap(false, true) {
		return
	}
	m.wg.Go(func() {
		defer m.playing.Store(false)
		defer crash.Recover("chatmedia play", func(p *crash.Panic) { report(p) })

		var reader io.ReaderAt
		var size int64
		var close func()
		var err error
		if source, ok := m.source.(interface {
			MediaStream(context.Context, model.Message) (io.ReaderAt, int64, func(), error)
		}); ok {
			reader, size, close, err = source.MediaStream(m.ctx, msg)
		} else {
			var data []byte
			data, err = m.source.Media(m.ctx, msg)
			reader = bytes.NewReader(data)
			size = int64(len(data))
			close = func() {}
		}
		if err != nil {
			report(err)
			return
		}
		defer close()
		stream, err := player.Serve("video", reportReader{reader, report}, size)
		if err != nil {
			report(err)
			return
		}
		defer stream.Close()
		args := kind.PrivateArgs()
		if msg.Media != nil {
			args = append(args, kind.SizeArgs(msg.Media.Width, msg.Media.Height)...)
		}
		p, err := player.Open(m.ctx, kind, path, stream.URL(), args...)
		if err != nil {
			report(err)
			return
		}
		defer p.Close()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				// A player that could not play the file says so, and may
				// keep its window open on nothing: the error is shown here.
				status := p.Status()
				if status.Err != nil {
					report(status.Err)
					return
				}
				if !status.Running {
					return
				}
			}
		}
	})
}

type reportReader struct {
	io.ReaderAt
	report func(error)
}

func (r reportReader) ReadAt(p []byte, off int64) (int, error) {
	n, e := r.ReaderAt.ReadAt(p, off)
	if e != nil && e != io.EOF && !errors.Is(e, context.Canceled) {
		r.report(e)
	}
	return n, e
}

type Status struct {
	Frame, Preview     image.Image
	Err                error
	Downloaded, Total  int64
	Loading, Cancelled bool
}

func (m *Manager) Status(msg model.Message, animate bool) Status {
	return m.StatusFit(msg, animate, image.Point{}, false)
}

// StatusFit is Status for media shown in want pixels. Still images are decoded
// to fit or cover; animated stickers keep a close, step-rounded pixel size.
func (m *Manager) StatusFit(msg model.Message, animate bool, want image.Point, cover bool) Status {
	frame, _ := m.frame(msg, animate, want, cover)
	if msg.Media == nil {
		return Status{}
	}
	e := m.entries[msg.Media.ID]
	if e == nil {
		return Status{Loading: true}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return Status{Frame: frame, Preview: e.preview, Err: e.err, Downloaded: e.downloaded, Total: e.total, Loading: e.frame == nil && e.err == nil && !e.cancelled && !e.waiting, Cancelled: e.cancelled}
}
func (m *Manager) Cancel(msg model.Message) {
	if msg.Media == nil {
		return
	}
	m.cancelled[msg.Media.ID] = true
	if e := m.entries[msg.Media.ID]; e != nil {
		e.mu.Lock()
		e.cancelled = true
		e.mu.Unlock()
		e.cancel()
	}
	m.changed()
}

// NewSized returns a manager keeping up to limit media, with still images
// decoded no larger than imageSize on either side.
func NewSized(source Source, changed func(), limit, imageSize int) *Manager {
	m := New(source, changed)
	m.limit, m.imageSize = limit, imageSize
	return m
}

func NewAvatars(source Source, changed func()) *Manager {
	m := New(source, changed)
	m.limit = 64
	m.imageSize = 160
	m.uiClips = true
	m.videoHeight = 160
	m.sem = make(chan struct{}, 2)
	return m
}

func (m *Manager) PauseAnimations() {
	for _, e := range m.entries {
		e.mu.Lock()
		e.animate = false
		e.mu.Unlock()
	}
}

// Stats runs on the owning UI goroutine. Bytes estimates decoded pixel storage,
// excluding codec runtimes, texture copies and shared frame deduplication.
func (m *Manager) Stats() (entries int, bytes int64) {
	entries = len(m.entries)
	for _, e := range m.entries {
		e.mu.Lock()
		bytes += e.bytes()
		e.mu.Unlock()
	}
	return
}

// bytes estimates the decoded pixels e holds. The caller holds e.mu.
func (e *entry) bytes() (bytes int64) {
	frames := []image.Image{e.frame, e.preview}
	if e.still != nil && e.still != e.frame && (e.clip == nil || e.still != e.clip.frames[0]) {
		frames = append(frames, e.still)
	}
	if e.clip != nil {
		bytes += e.clip.bytes
		// The current frame already belongs to the cached loop.
		frames = frames[1:]
	}
	for _, im := range frames {
		bytes += imageBytes(im)
	}
	return bytes
}

// imageBytes estimates the pixel storage of im.
func imageBytes(im image.Image) int64 {
	switch im := nilImage(im).(type) {
	case *image.RGBA:
		return int64(len(im.Pix))
	case *image.NRGBA:
		return int64(len(im.Pix))
	case *image.YCbCr:
		return int64(len(im.Y) + len(im.Cb) + len(im.Cr))
	case nil:
		return 0
	default:
		size := im.Bounds().Size()
		return int64(size.X) * int64(size.Y) * 4
	}
}

// NewShared keeps a few screens of thumbnails, bounded by both count and pixels.
// Off-screen animations pause immediately; cached loops survive brief scrolls.
func NewShared(source Source, changed func()) *Manager {
	m := NewSized(source, changed, 160, 512)
	m.videoHeight = 256
	m.retainAnimations = 15 * time.Second
	m.byteLimit = 96 << 20
	return m
}
func (m *Manager) trimBytes() {
	if m.byteLimit == 0 {
		return
	}
	for {
		_, bytes := m.Stats()
		// Loops and large pictures go before the stills of stickers.
		if bytes <= m.byteLimit || !m.evict(true) && !m.evict(false) {
			return
		}
	}
}

func (m *Manager) trimAnimations() {
	if m.retainAnimations == 0 {
		return
	}
	for {
		active := 0
		var oldest *entry
		var when time.Time
		for _, e := range m.entries {
			e.mu.Lock()
			if e.animated && !e.expired {
				active++
				if e.seen+1 < m.generation && (oldest == nil || e.lastVisible.Before(when)) {
					oldest, when = e, e.lastVisible
				}
			}
			e.mu.Unlock()
		}
		if active <= 24 || oldest == nil {
			return
		}
		oldest.mu.Lock()
		oldest.cancel()
		oldest.expired = true
		oldest.mu.Unlock()
	}
}
