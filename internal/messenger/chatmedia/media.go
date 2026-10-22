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
	"slices"
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
	mu    sync.Mutex
	frame image.Image
	// textColor: the custom emoji is drawn in the text's color; see
	// TextColored.
	textColor bool
	still     image.Image
	wasm      bool
	ffmpeg    string
	// sandboxGIF plays the GIF with the H.264 decoder in its sandbox, for
	// want of an FFmpeg.
	sandboxGIF bool
	wake       chan struct{}
	// check is the ticker offer waits for a picture to be taken with, kept
	// rather than made for each picture; only the entry's worker uses it.
	check             *time.Ticker
	clip              *stickerClip
	err               error
	seen              uint64
	animated          bool
	caching           bool
	animate           bool
	played            uint64 // the frame a view last asked e to animate in
	heldAt            uint64 // the frame a view last held e in
	held              bool   // stopped where it is, to play on from there
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
	// pending is the next picture of an animation a worker plays for a
	// manager on the frame's clock, to show from pendingAt; consumed tells
	// the worker it was taken. worker is set while the worker plays it.
	pending   image.Image
	pendingAt time.Time
	consumed  chan struct{}
	worker    bool
	// every is every how many ticks the worker shows a picture.
	every int64
	// fullRate lets the animation show 60 pictures a second.
	fullRate bool
	// position is where in its loop a GIF's worker is, and seekTo, when
	// seekSet, where it is to go next (SeekGIF).
	position time.Duration
	seekTo   time.Duration
	seekSet  bool
}

// resumeAt is where a GIF's worker that starts plays from: where it was
// asked to go, or where it stood, held; ok is false for the start.
func (e *entry) resumeAt() (at time.Duration, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.seekSet {
		e.seekSet = false
		return e.seekTo, true
	}
	return e.position, e.held && e.position > 0
}

// takeSeek is where a GIF's worker is asked to go, once.
func (e *entry) takeSeek() (time.Duration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.seekSet {
		return 0, false
	}
	e.seekSet = false
	return e.seekTo, true
}

// setPosition is where the GIF's worker is now.
func (e *entry) setPosition(at time.Duration) {
	e.mu.Lock()
	e.position = at
	e.mu.Unlock()
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
	// played is the message the external player plays, and player the
	// player once it is open, for a timestamp to move it; playMu guards
	// both.
	playMu sync.Mutex
	played model.MessageKey
	player player.Player
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
	// ffmpegGIFs are the GIFs, by media ID, the user chose to play with
	// FFmpeg, out of the sandbox, which cannot play them
	// (PlayGIFWithFFmpeg).
	ffmpegGIFs map[string]bool
	// paused stops every animation where it is; UI thread only.
	paused bool
	// holding stops the media asked for where they are; UI thread only.
	holding bool
	// stopped are the GIFs the user paused, and seeks where those not
	// loaded are to start, by media ID; UI thread only.
	stopped map[string]bool
	seeks   map[string]time.Duration
	// uiClips plays decoded loops on the UI's clock: each frame shows the
	// picture of its own time, and the next is scheduled by NextFrame, so
	// that any number of loops wake the window once per picture. Emoji
	// statuses, few, small and often on screen, play so. now is the time of
	// the frame being drawn, next when it is to be drawn again.
	uiClips   bool
	now, next time.Time
	// clocked is set while the view draws on the frame's clock and asks
	// for NextFrame: workers then leave their pictures for the frames due,
	// instead of waking the window for each.
	clocked atomic.Bool
}

func New(source Source, changed func()) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{ctx: ctx, cancel: cancel, source: source, changed: changed, limit: 24, imageSize: 1024, videoHeight: 384, byteLimit: 96 << 20, entries: map[string]*entry{}, cancelled: map[string]bool{}, sem: make(chan struct{}, 4)}
	m.lottie.open = lottie.NewRuntime
	m.vp9.open = vp9.NewRuntime
	m.avc.open = newAVCRuntime
	return m
}

// TextColored tells whether msg's custom emoji is drawn in the color of
// the text around it: its frames are its shape, for the caller to color.
// It is known by the time its first frame is.
func (m *Manager) TextColored(msg model.Message) bool {
	if msg.Media == nil {
		return false
	}
	e := m.entries[msg.Media.ID]
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.textColor
}

func (m *Manager) Frame(msg model.Message, animate bool) (image.Image, error) {
	return m.frame(msg, animate, image.Point{}, false, false)
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

func (m *Manager) frame(msg model.Message, animate bool, want image.Point, cover, fullRate bool) (image.Image, error) {
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
		e = &entry{wake: make(chan struct{}, 1), consumed: make(chan struct{}, 1), wasm: wasm, ffmpeg: m.ffmpeg, sandboxGIF: m.SandboxGIFs() && !m.ffmpegGIFs[key], seen: m.generation, animate: animate, cancel: cancel, cancelled: m.cancelled[key], frame: previous, preview: preview, still: still, clip: clip, box: box, cover: cover, sticker: animatedSticker || msg.Kind == model.MessageSticker}
		m.entries[key] = e
		if at, ok := m.seeks[key]; ok {
			delete(m.seeks, key)
			e.seekTo, e.seekSet = at, true
		}
		// The loader takes a copy of its own: captured, msg itself would be
		// moved to the heap on every call, every frame, for each picture.
		loading := msg
		m.wg.Go(func() {
			defer crash.Recover("chatmedia load "+key, func(p *crash.Panic) {
				e.mu.Lock()
				e.err = p
				e.mu.Unlock()
				m.changed()
			})
			m.load(ctx, e, loading)
		})
	}
	hold := m.paused || m.holding || m.stopped[key]
	if hold {
		animate = false
		e.heldAt = m.generation
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	wasAnimating := e.animate
	// A view of the same media drawn still, before or after one that plays
	// it, does not stop it: stopped for a moment, the animation would start
	// over. EndFrame stops what no view of the frame played.
	if animate {
		e.animate, e.played = true, m.generation
	}
	if e.animate && !wasAnimating {
		select {
		case e.wake <- struct{}{}:
		default:
		}
	}
	e.seen = m.generation
	e.lastVisible = time.Now()
	e.fullRate = e.fullRate || fullRate
	if m.clocked.Load() && e.worker {
		m.workerFrame(e)
	}
	if m.uiClips && e.clip != nil {
		if im := m.clipFrame(e); im != nil {
			return im, e.err
		}
	}
	// A paused animation stays on the frame it reached.
	if !animate && e.still != nil && (!hold || e.frame == nil) {
		return e.still, e.err
	}
	return e.frame, e.err
}

// SetPaused stops every animation where it is, or lets them play again, as
// Telegram Desktop pauses them while its window is not active. It runs on
// the UI goroutine.
func (m *Manager) SetPaused(paused bool) { m.paused = paused }

// ToggleGIF pauses the GIF of msg where it is, or lets it play on from
// there, as a click on it does. It runs on the UI goroutine.
func (m *Manager) ToggleGIF(msg model.Message) {
	if msg.Media == nil {
		return
	}
	key := msg.Media.ID
	if m.stopped[key] {
		delete(m.stopped, key)
		return
	}
	if m.stopped == nil {
		m.stopped = map[string]bool{}
	}
	m.stopped[key] = true
}

// GIFPaused reports whether the user paused the GIF of msg. It runs on the
// UI goroutine.
func (m *Manager) GIFPaused(msg model.Message) bool {
	return msg.Media != nil && m.stopped[msg.Media.ID]
}

// SeekGIF moves the GIF of msg to at, a time in its loop, as a timestamp
// or its bar asks: with play a paused one plays again from there, as for
// a timestamp; without, a paused one stays paused and shows the picture
// at at, as for its bar. It runs on the UI goroutine.
func (m *Manager) SeekGIF(msg model.Message, at time.Duration, play bool) {
	if msg.Media == nil {
		return
	}
	key := msg.Media.ID
	if play {
		delete(m.stopped, key)
	}
	e := m.entries[key]
	if e == nil {
		if m.seeks == nil {
			m.seeks = map[string]time.Duration{}
		}
		m.seeks[key] = at
		return
	}
	e.mu.Lock()
	e.seekTo, e.seekSet, e.position = at, true, at
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// GIFPosition is where in its loop the GIF of msg plays, or stands; 0
// before it played. It runs on the UI goroutine.
func (m *Manager) GIFPosition(msg model.Message) time.Duration {
	if msg.Media == nil {
		return 0
	}
	e := m.entries[msg.Media.ID]
	if e == nil {
		return m.seeks[msg.Media.ID]
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.position
}

// SetHolding makes the media asked for until SetHolding(false) stand on the
// pictures they reached, as SetPaused does, rather than on their first: a
// view laid under a dialog holds them so, and they play on from there once
// asked to play again. It runs on the UI goroutine.
func (m *Manager) SetHolding(holding bool) { m.holding = holding }

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
	now := tickFloor(m.now, 2)
	if e.clipStart.IsZero() {
		// A loop that stood still goes on from where it stopped.
		e.clipStart = now.Add(-e.clipAt)
	}
	step := e.clip.frameDuration
	elapsed := now.Sub(e.clipStart)
	index := pictureAt(elapsed, step)
	e.clipAt = elapsed % (step * time.Duration(len(e.clip.frames)))
	e.frame = e.clip.frames[index%int64(len(e.clip.frames))]
	m.schedule(pictureTick(e.clipStart, index+1, step, 2))
	return e.frame
}

// workerFrame shows the picture the worker left for this frame, if it is
// due, and schedules the frame of the next one. The caller holds e.mu.
func (m *Manager) workerFrame(e *entry) {
	if e.pending != nil && !m.now.Before(e.pendingAt) {
		e.frame, e.pending = e.pending, nil
		select {
		case e.consumed <- struct{}{}:
		default:
		}
	}
	if !e.animate {
		return
	}
	if e.pending != nil {
		m.schedule(e.pendingAt)
		return
	}
	// The worker is drawing it: it is due on its next tick at the soonest.
	m.schedule(tickCeil(m.now.Add(time.Nanosecond), max(e.every, 1)))
}

// schedule asks for a frame at t, if none is asked for sooner.
func (m *Manager) schedule(t time.Time) {
	if m.next.IsZero() || t.Before(m.next) {
		m.next = t
	}
}

// Visibility is tied to completed UI frames, not elapsed time. An idle window
// may go minutes without a frame while every image is still on screen.
// A view that begins frames so draws when it is woken: the workers playing
// animations wake it for each picture.
func (m *Manager) BeginFrame() {
	m.clocked.Store(false)
	m.begin(time.Now())
}

// BeginFrameAt begins a frame drawn for now, on the frame's clock: the
// pictures of animations are those of now, and the view asks NextFrame when
// to draw again, instead of being woken for each picture.
func (m *Manager) BeginFrameAt(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	m.clocked.Store(true)
	m.begin(now)
}

func (m *Manager) begin(now time.Time) {
	m.generation++
	m.now, m.next = now, time.Time{}
}

// NextFrame is when an animation drawn in the frame shows its next picture,
// zero for never: the window is to draw a frame again then. It runs on the
// UI goroutine, after the frame's media were asked for.
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
		if e.played != m.generation {
			e.animate = false
			e.held = e.heldAt == m.generation
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

// FFmpegFound reports whether an FFmpeg is there to play a GIF with: the
// one the settings name, or one on the system.
func (m *Manager) FFmpegFound() bool {
	return video.ResolveFFmpeg(m.ffmpeg) != ""
}

// PlayGIFWithFFmpeg plays the GIF of msg with FFmpeg, out of the sandbox,
// which cannot play it (mp4.ErrUnsupported), as the user chose to: the
// other GIFs stay in the sandbox.
func (m *Manager) PlayGIFWithFFmpeg(msg model.Message) {
	if msg.Media == nil {
		return
	}
	if m.ffmpegGIFs == nil {
		m.ffmpegGIFs = map[string]bool{}
	}
	m.ffmpegGIFs[msg.Media.ID] = true
	m.Retry(msg)
	m.changed()
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
		}, e.clip.frameDuration, publish)
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
			if err == nil && msg.Media != nil {
				e.mu.Lock()
				e.textColor = msg.Media.TextColor
				e.mu.Unlock()
			}
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
	// period is how long a frame of the animation lasts.
	var period time.Duration
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
				var ring pictureRing
				frame = func(t time.Duration) (image.Image, error) {
					if dst := ring.next(m, e, a.Size); dst != nil {
						_, er := a.FrameInto(ctx, t, dst)
						return dst, er
					}
					im, _, er := a.FrameAt(ctx, t)
					return clone(im), er
				}
				period = time.Duration(float64(time.Second) / a.FPS)
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
			if f, d, ok := m.ffmpegSticker(ctx, e, data); ok {
				frame, period = f, d
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
						period = clip.frameDuration
					}
				} else {
					var lastIndex = -1
					var last image.Image
					var ring pictureRing
					size := resample.Fit(a.Size.X, a.Size.Y, e.box, false)
					frame = func(t time.Duration) (image.Image, error) {
						index := int(t/a.File.FrameDuration) % a.FrameCount()
						if index == lastIndex {
							return last, nil
						}
						var im *image.RGBA
						var er error
						if dst := ring.next(m, e, size); dst != nil {
							im, er = a.FrameSizedInto(ctx, t, dst)
						} else {
							im, er = stickerFrame(ctx, a, t, e.box)
						}
						if er != nil {
							return nil, er
						}
						last = im
						lastIndex = index
						return last, nil
					}
					period = a.File.FrameDuration
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
		m.playGIF(ctx, e, stream.URL(), msg.Media.Duration, publish)
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
	m.playAnimation(ctx, e, frame, period, publish)
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

// playGIF plays a GIF from url, of length long, whenever it is to animate,
// with an ffmpeg that it stops once the GIF has not animated for gifIdle.
// The file is probed for the size of its frames once, when it first plays.
// A GIF held where it stood plays on from there, and one asked to go
// elsewhere starts an ffmpeg there.
func (m *Manager) playGIF(ctx context.Context, e *entry, url string, long time.Duration, publish func(image.Image, error)) {
	var info video.Info
	seek := time.Duration(-1)
	for {
		// Asked to go elsewhere, it goes, still or not.
		if seek < 0 {
			if err := waitAnimation(ctx, e); err != nil {
				return
			}
		}
		from := seek
		if from < 0 {
			if at, ok := e.resumeAt(); ok {
				from = at
			}
		}
		// Standing still, it shows the picture there.
		e.mu.Lock()
		still := !e.animate
		e.mu.Unlock()
		var p *video.Player
		if info.Width == 0 {
			var err error
			p, err = video.NewPlayerWithFFmpeg(url, m.videoHeight, e.ffmpeg)
			if err != nil {
				publish(nil, err)
				return
			}
			info = p.Info()
		} else {
			p = video.NewPlayerAs(url, info, e.ffmpeg)
		}
		if long > 0 && from >= long {
			from = 0
		}
		if still {
			// The picture there alone, from an FFmpeg that decodes it and
			// stops: one that plays would read ahead of it.
			seek = -1
			frames, err := video.NewFrames(url, e.ffmpeg, max(from, 0), image.Pt(info.Width, info.Height), image.Pt(info.Width, info.Height), max(info.FPS, 1))
			if err != nil {
				publish(nil, err)
				return
			}
			im, at, err := frames.Next()
			frames.Close()
			switch {
			case err == nil:
				if long > 0 {
					at %= long
				}
				e.setPosition(at)
				showStill(e, im, publish)
			case !errors.Is(err, io.EOF):
				publish(nil, err)
				return
			}
			continue
		}
		if from > 0 {
			p.StartAt(from)
		}
		p.Start()
		var err error
		seek, err = m.playGIFFrames(ctx, e, p, long, publish)
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
		from, resume := e.resumeAt()
		if !resume {
			from = -1
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
		err = m.playVideoFrames(ctx, e, v, from, publish)
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

// showStill shows im, a picture of e that stands still, at once, over a
// picture of the animation left for a frame.
func showStill(e *entry, im image.Image, publish func(image.Image, error)) {
	e.mu.Lock()
	e.pending = nil
	e.mu.Unlock()
	publish(im, nil)
}

// playVideoFrames plays the pictures of v, from from (below 0, the start),
// each on the tick of the clock every animation shares that is its time,
// while the GIF animates, and returns once it has not for gifIdle. Asked to
// go elsewhere, it goes on from there; standing still, it shows the picture
// there.
func (m *Manager) playVideoFrames(ctx context.Context, e *entry, v *h264.Video, from time.Duration, publish func(image.Image, error)) error {
	size := resample.Fit(v.Size().X, v.Size().Y, e.box, e.cover)
	defer m.byWorker(e)()
	var ring pictureRing
	var idle, start time.Time
	for {
		e.mu.Lock()
		animate := e.animate
		e.mu.Unlock()
		if at, ok := e.takeSeek(); ok {
			from = at
		}
		if from >= 0 && !animate {
			i, err := seekPicture(ctx, v, from)
			if err != nil {
				return err
			}
			e.setPosition(v.Time(i))
			showStill(e, v.Picture(size), publish)
			// Played on, it goes on from there.
			from, start, idle = -1, time.Time{}, time.Now()
		}
		if !animate {
			start = time.Time{}
			if idle.IsZero() {
				idle = time.Now()
			} else if time.Since(idle) > gifIdle {
				return nil
			}
			timer := time.NewTimer(time.Second / 30)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			case <-e.wake:
				// Animation asked for again: the paused GIF resumes at once.
				timer.Stop()
			}
			continue
		}
		idle = time.Time{}
		var i int
		var err error
		if from >= 0 {
			i, err = seekPicture(ctx, v, from)
			from, start = -1, time.Time{}
		} else {
			i, err = v.Next(ctx)
		}
		if err != nil {
			return err
		}
		e.setPosition(v.Time(i))
		var im *image.RGBA
		if dst := ring.next(m, e, size); dst != nil {
			im = v.PictureInto(dst)
		} else {
			im = v.Picture(size)
		}
		soonest := tickCeil(time.Now().Add(time.Nanosecond), 2)
		if i == 0 || start.IsZero() {
			// A loop, or a pause, starts the clock again.
			start = soonest.Add(-v.Time(i))
		}
		at := tickCeil(start.Add(v.Time(i)-frameTick/2), 2)
		if at.Before(soonest) {
			// Behind time, the next picture follows at once: a slow machine
			// plays slower rather than skipping, which H.264 cannot.
			start = start.Add(soonest.Sub(at))
			at = soonest
		}
		if !m.offer(ctx, e, im, at, 2, true, publish) {
			return nil
		}
	}
}

// seekPicture decodes the picture of v that shows at at, or the last one,
// and returns its index.
func seekPicture(ctx context.Context, v *h264.Video, at time.Duration) (int, error) {
	if last := v.Pictures() - 1; last >= 0 && at > v.Time(last) {
		at = v.Time(last)
	}
	if _, err := v.Seek(ctx, at); err != nil {
		return 0, err
	}
	for {
		i, err := v.Next(ctx)
		if err != nil || i+1 >= v.Pictures() || v.Time(i+1) > at {
			return i, err
		}
	}
}

// playGIFFrames shows the frames p plays, which ffmpeg paces itself, while
// the GIF of length long animates, and returns once it has not for
// gifIdle, or once it is asked to go elsewhere: then with where, else -1.
// It looks for a new frame on every tick of the clock every animation
// shares a GIF shows one on, and leaves it for that tick.
func (m *Manager) playGIFFrames(ctx context.Context, e *entry, p *video.Player, long time.Duration, publish func(image.Image, error)) (time.Duration, error) {
	defer m.byWorker(e)()
	var idle time.Time
	var last *image.RGBA
	// offered are the frames left for the view, given back to p once they
	// are not shown, as pictureRing does with its own.
	var offered []*image.RGBA
	for {
		e.mu.Lock()
		animate := e.animate
		e.mu.Unlock()
		if at, ok := e.takeSeek(); ok {
			return at, nil
		}
		p.SetPaused(!animate)
		if m.clocked.Load() {
			offered = slices.DeleteFunc(offered, func(im *image.RGBA) bool {
				if e.uses(im.Pix) {
					return false
				}
				p.Release(im)
				return true
			})
		}
		tick := tickFloor(time.Now(), 2)
		if animate {
			idle = time.Time{}
			im, err := p.Frame()
			if err != nil {
				return -1, err
			}
			if im != nil && im != last {
				last = im
				at := p.Position()
				if long > 0 {
					at %= long
				}
				e.setPosition(at)
				m.offer(ctx, e, im, tick, 2, false, publish)
				offered = append(offered, im)
			}
		} else if idle.IsZero() {
			idle = time.Now()
		} else if time.Since(idle) > gifIdle {
			return -1, nil
		}
		timer := time.NewTimer(time.Until(tick.Add(2 * frameTick)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return -1, nil
		case <-timer.C:
		}
	}
}

// byWorker marks e as played by a worker, which offers its pictures, until
// the returned function is called.
func (m *Manager) byWorker(e *entry) func() {
	e.mu.Lock()
	e.animated, e.worker = true, true
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		e.worker, e.pending = false, nil
		e.mu.Unlock()
	}
}

// offer shows im, a picture of e, from at, a tick of every k-th. For a view
// on the frame's clock the picture is left for the frame due, and with wait
// offer returns once the frame took it, or e stopped animating; otherwise
// it wakes the window at at. It reports false once ctx is done.
func (m *Manager) offer(ctx context.Context, e *entry, im image.Image, at time.Time, k int64, wait bool, publish func(image.Image, error)) bool {
	if !m.clocked.Load() {
		if d := time.Until(at); d > 0 {
			timer := time.NewTimer(d)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		}
		publish(im, nil)
		return true
	}
	e.mu.Lock()
	e.pending, e.pendingAt, e.every = im, at, k
	e.mu.Unlock()
	if !wait {
		return true
	}
	// Stopped, it is not taken: the worker is to see it has to wait.
	if e.check == nil {
		e.check = time.NewTicker(gifIdle / 4)
	} else {
		e.check.Reset(gifIdle / 4)
	}
	check := e.check
	defer check.Stop()
	for {
		select {
		case <-e.consumed:
			return true
		case <-ctx.Done():
			return false
		case <-check.C:
			e.mu.Lock()
			animate := e.animate
			e.mu.Unlock()
			if !animate {
				return true
			}
		}
	}
}

// waitAnimation blocks until e is to animate, or to show another picture of
// a GIF that stands still (SeekGIF).
func waitAnimation(ctx context.Context, e *entry) error {
	for {
		e.mu.Lock()
		animate := e.animate || e.seekSet
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

// playAnimation plays the animation frame draws, whose own frames last
// period: its first picture at once, and the others, while it is to
// animate, on the ticks of the clock every animation shares, each drawn
// ahead of its tick. For a view on the frame's clock the picture is left for
// the frame due, which shows it and schedules the next; otherwise it wakes
// the window when due.
func (m *Manager) playAnimation(ctx context.Context, e *entry, frame func(time.Duration) (image.Image, error), period time.Duration, publish func(image.Image, error)) {
	defer m.byWorker(e)()
	im, err := frame(0)
	if err != nil {
		publish(nil, err)
		return
	}
	publish(im, nil)
	var started time.Time
	// resume is the picture a held animation plays on from.
	shown, resume := int64(-1), int64(0)
	for {
		e.mu.Lock()
		animate, fullRate := e.animate, e.fullRate
		e.mu.Unlock()
		if !animate {
			if waitAnimation(ctx, e) != nil {
				return
			}
			e.mu.Lock()
			held := e.held
			e.mu.Unlock()
			// Played again, it starts over, unless it was held where it is:
			// then it goes on from the picture after the one shown.
			resume = 0
			if held && shown >= 0 {
				resume = shown + 1
			}
			started, shown = time.Time{}, resume-1
			continue
		}
		k := ticksPer(period, fullRate)
		step := period
		if step <= 0 {
			step = time.Duration(k) * frameTick
		}
		soonest := tickCeil(time.Now().Add(time.Nanosecond), k)
		if started.IsZero() {
			started = soonest.Add(-step * time.Duration(resume))
			resume = 0
		}
		// The tick of the next picture that differs from the one shown: a
		// slow animation skips the ticks that would show the same.
		at := pictureTick(started, shown+1, step, k)
		if at.Before(soonest) {
			at = soonest
		}
		index := pictureAt(at.Sub(started), step)
		// The middle of the picture's frame, which no rounding moves.
		im, err := frame(step*time.Duration(index) + step/2)
		if err != nil {
			publish(nil, err)
			return
		}
		shown = index
		if !m.offer(ctx, e, im, at, k, true, publish) {
			return
		}
	}
}

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
	m.PlayFrom(msg, kind, path, -1, report)
}

// PlayFrom is Play from start, a time from the start of the file; below 0,
// from the start, or, when msg plays already, from where it is. A
// message that plays already moves to start.
func (m *Manager) PlayFrom(msg model.Message, kind player.Kind, path string, start time.Duration, report func(error)) {
	if !m.playing.CompareAndSwap(false, true) {
		m.playMu.Lock()
		p, played := m.player, m.played
		m.playMu.Unlock()
		if start >= 0 && p != nil && played == msg.Key {
			if err := p.SeekTo(start); err != nil {
				report(err)
			}
		}
		return
	}
	m.playMu.Lock()
	m.played = msg.Key
	m.playMu.Unlock()
	m.wg.Go(func() {
		defer m.playing.Store(false)
		defer func() {
			m.playMu.Lock()
			m.played, m.player = model.MessageKey{}, nil
			m.playMu.Unlock()
		}()
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
		args = append(args, kind.StartArgs(start)...)
		p, err := player.Open(m.ctx, kind, path, stream.URL(), args...)
		if err != nil {
			report(err)
			return
		}
		defer p.Close()
		m.playMu.Lock()
		m.player = p
		m.playMu.Unlock()
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
	return m.status(msg, animate, want, cover, false)
}

// StatusFullRate is StatusFit for media whose animation shows up to 60
// pictures a second, as Telegram Desktop plays stickers in messages; others
// show 30.
func (m *Manager) StatusFullRate(msg model.Message, animate bool, want image.Point, cover bool) Status {
	return m.status(msg, animate, want, cover, true)
}

func (m *Manager) status(msg model.Message, animate bool, want image.Point, cover, fullRate bool) Status {
	frame, _ := m.frame(msg, animate, want, cover, fullRate)
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
	// A picture of a cached loop is counted with the loop.
	if e.pending != nil && e.pending != e.frame && e.clip == nil {
		bytes += imageBytes(e.pending)
	}
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
