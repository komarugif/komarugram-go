// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// galleryStore has photos 10, 20, … 300 in chat 1, each with "m" and "x"
// variants, and records which files were read.
type galleryStore struct {
	benchmarkHistory
	photos []model.Message
	mu     sync.Mutex
	reads  []string
	// hold, when set, keeps originals downloading until it is closed.
	hold    chan struct{}
	encoded map[image.Point][]byte
	// cached pages tell no total, as those of the offline cache.
	cached bool
}

func newGalleryStore() *galleryStore {
	s := &galleryStore{}
	for id := 10; id <= 300; id += 10 {
		media := &model.MessageMedia{ID: fmt.Sprintf("p%d/w", id), Width: 1600, Height: 1200, Variants: []model.MessageMedia{{ID: fmt.Sprintf("p%d/m", id), Width: 320, Height: 240}, {ID: fmt.Sprintf("p%d/x", id), Width: 800, Height: 600}}}
		s.photos = append(s.photos, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(id)}, Kind: model.MessagePhoto, Media: media, Date: time.Unix(int64(id), 0)})
	}
	return s
}

func (s *galleryStore) ChatPhotos(_ context.Context, chat int64, anchor model.MessageID, dir, limit int) (model.PhotoPage, error) {
	var out []model.Message
	for _, m := range s.photos {
		if dir < 0 && m.Key.MessageID < anchor || dir > 0 && m.Key.MessageID > anchor {
			out = append(out, m)
		}
	}
	page := model.PhotoPage{Total: len(s.photos), More: len(out) > limit}
	if s.cached {
		page.Total = 0
	}
	if dir < 0 {
		page.Messages = out[max(0, len(out)-limit):]
	} else {
		page.Messages = out[:min(len(out), limit)]
	}
	return page, nil
}

func (s *galleryStore) Media(ctx context.Context, m model.Message) ([]byte, error) {
	s.mu.Lock()
	s.reads = append(s.reads, m.Media.ID)
	hold := s.hold
	s.mu.Unlock()
	if hold != nil && strings.HasSuffix(m.Media.ID, "/w") {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	size := image.Pt(m.Media.Width, m.Media.Height)
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.encoded[size]; ok {
		return b, nil
	}
	// Uncompressed and once per size: the race detector makes compressing
	// a 1600×1200 picture take seconds.
	var b bytes.Buffer
	// An opaque grey picture, as photos are.
	im := image.NewRGBA(image.Rectangle{Max: size})
	for i := range im.Pix {
		im.Pix[i] = 0x80 | uint8(i%4/3)*0x7f
	}
	err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&b, im)
	if s.encoded == nil {
		s.encoded = map[image.Point][]byte{}
	}
	s.encoded[size] = b.Bytes()
	return b.Bytes(), err
}

func (s *galleryStore) read() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reads...)
}

type viewerHarness struct {
	t       *testing.T
	router  input.Router
	viewer  *photoViewer
	images  imageOps
	store   *galleryStore
	now     time.Time
	changed chan struct{}
}

func newViewerHarness(t *testing.T) *viewerHarness {
	h := &viewerHarness{t: t, store: newGalleryStore(), now: time.Unix(1000, 0), changed: make(chan struct{}, 64)}
	h.viewer = newPhotoViewer(h.store, &h.images, func() {
		select {
		case h.changed <- struct{}{}:
		default:
		}
	})
	t.Cleanup(h.viewer.Destroy)
	return h
}

func (h *viewerHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(800, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	h.images.BeginFrame()
	h.viewer.Layout(gtx, localization.For("en"), false)
	h.images.EndFrame()
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(16 * time.Millisecond)
}

// until draws frames until cond holds, waiting for background work between them.
func (h *viewerHarness) until(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.After(3 * time.Second)
	for h.frame(); !cond(); h.frame() {
		select {
		case <-h.changed:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (h *viewerHarness) click(x, y float32) {
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(x, y), Buttons: pointer.ButtonPrimary, Time: time.Duration(h.now.UnixNano())})
		h.frame()
	}
}

func (h *viewerHarness) key(name key.Name) {
	h.router.Queue(key.Event{Name: name, State: key.Press})
	h.frame()
}

func (h *viewerHarness) count() int {
	items, _, _, _, _ := h.viewer.snapshot()
	return len(items)
}

// Layout at 800×600 and 1 px per dp: the photo stage spans x 72…728 and
// y 56…516, the arrows are centred at x 36 and 764, y 286, the close button
// at 768, 28, and thumbnails 56 px wide at y 530…586 every 62 px.
func TestPhotoViewerNavigatesCachedPhotos(t *testing.T) {
	h := newViewerHarness(t)
	s := h.store
	h.viewer.Open(1, s.photos[14], s.photos[13:16])
	h.until("the gallery pages", func() bool { return h.count() == len(s.photos) })
	if h.viewer.current != 150 {
		t.Fatalf("opened on %d", h.viewer.current)
	}
	h.until("the photo to decode", func() bool {
		return h.viewer.full.StatusFit(s.photos[14], false, image.Pt(656, 460), false).Frame != nil
	})
	frame := h.viewer.full.StatusFit(s.photos[14], false, image.Pt(656, 460), false).Frame
	// Decoded for the stage rounded up to 768×512, not at the original 1600×1200.
	if got := frame.Bounds().Size(); got != image.Pt(683, 512) {
		t.Fatalf("decoded at %v", got)
	}

	h.key(key.NameRightArrow)
	if h.viewer.current != 160 {
		t.Fatalf("right arrow key: %d", h.viewer.current)
	}
	h.click(764, 286)
	if h.viewer.current != 170 {
		t.Fatalf("next button: %d", h.viewer.current)
	}
	h.click(36, 286)
	if h.viewer.current != 160 {
		t.Fatalf("previous button: %d", h.viewer.current)
	}
	h.key(key.NameHome)
	h.click(36, 286) // no previous photo: the backdrop is there and closes the viewer
	if h.viewer.open {
		t.Fatal("click on the empty backdrop kept the viewer open")
	}
	if n, _ := h.viewer.full.Stats(); n != 0 {
		t.Fatalf("closed viewer keeps %d decoded photos", n)
	}

	h.viewer.Open(1, s.photos[14], nil)
	h.until("the gallery pages", func() bool { return h.count() == len(s.photos) })
	h.frame()
	// Photo 15 (id 160 at index 15) centred: the strip starts at index 9.
	first := h.viewer.strip.Position.First
	h.click(float32(2*62+28), 558)
	if want := s.photos[first+2].Key.MessageID; h.viewer.current != want {
		t.Fatalf("thumbnail click selected %d, want %d", h.viewer.current, want)
	}
	// The frame that handled the click already shows the new photo; nothing
	// else would draw one until the next input.
	if h.viewer.drawn != h.viewer.current {
		t.Fatalf("after a thumbnail click the frame drew %d, not %d", h.viewer.drawn, h.viewer.current)
	}
	selected := h.viewer.current

	before := h.viewer.strip.Position
	pos := f32.Pt(400, 558)
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: pos, Buttons: pointer.ButtonPrimary})
	h.frame()
	for i := 0; i < 10; i++ {
		pos.X -= 25
		h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos, Buttons: pointer.ButtonPrimary})
		h.frame()
	}
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos})
	h.frame()
	after := h.viewer.strip.Position
	if after.First*62+after.Offset-(before.First*62+before.Offset) != 250 {
		t.Fatalf("drag moved the strip from %+v to %+v", before, after)
	}
	if h.viewer.current != selected {
		t.Fatal("a drag over the strip selected a thumbnail")
	}
	h.router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(300, 558), Scroll: f32.Pt(0, -120)})
	h.frame()
	if h.viewer.strip.Position == after {
		t.Fatal("the wheel did not scroll the strip")
	}

	// Thumbnails use the small variant. Originals, and the middle variant
	// shown while one loads, are read only for the photos shown (150, 160,
	// 170, 10, then 150 and the clicked one) and their neighbours, which are
	// decoded ahead.
	shown := map[string]bool{}
	for _, id := range []model.MessageID{150, 160, 170, 10, selected} {
		for _, n := range []model.MessageID{id - 10, id, id + 10} {
			shown[fmt.Sprintf("p%d", n)] = true
		}
	}
	thumbs := 0
	for _, id := range s.read() {
		if strings.HasSuffix(id, "/m") {
			thumbs++
		} else if photo := id[:strings.IndexByte(id, '/')]; !shown[photo] {
			t.Fatalf("read %s, which was never on screen", id)
		}
	}
	if thumbs < 10 {
		t.Fatalf("read %d thumbnails", thumbs)
	}
	h.key(key.NameEscape)
	if h.viewer.open {
		t.Fatal("Escape did not close the viewer")
	}
}

// A chat tile downloads the smallest variant that covers it and opens the
// viewer when clicked after loading.
func TestPhotoTileUsesVariantAndOpensViewer(t *testing.T) {
	s := newGalleryStore()
	photo := s.photos[0]
	p := newChatPage(benchmarkHistory{h: model.History{Messages: []model.Message{photo}, Revision: 1}}, func() {})
	p.media.Close()
	changed := make(chan struct{}, 8)
	p.source = s
	p.media = newTestMedia(s, changed)
	t.Cleanup(p.Close)
	var opened []model.Message
	p.openPhoto = func(m model.Message) { opened = append(opened, m) }
	var images imageOps
	p.images = &images
	r := &messageRow{}
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(800, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	tile := func() {
		gtx.Ops.Reset()
		p.media.BeginFrame()
		p.mediaTile(gtx, r, photo, image.Pt(300, 225), false, localization.For("en"), false)
		p.media.EndFrame()
	}
	deadline := time.After(3 * time.Second)
	for tile(); p.media.StatusFit(r.tile, false, image.Pt(300, 225), false).Frame == nil; tile() {
		select {
		case <-changed:
		case <-deadline:
			t.Fatal("tile did not load")
		}
	}
	if got := s.read(); len(got) != 1 || got[0] != "p10/m" {
		t.Fatalf("a 300×225 tile read %v, want the 320×240 variant", got)
	}
	r.media.Click()
	tile()
	if len(opened) != 1 || opened[0].Media.ID != "p10/w" {
		t.Fatalf("viewer opened with %+v", opened)
	}
}

func newTestMedia(s chatmedia.Source, changed chan struct{}) *chatmedia.Manager {
	return chatmedia.New(s, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
}

// While an original downloads, the viewer shows the variant covering half
// the stage, and a progress ring only once the wait is noticeable.
func TestPhotoViewerShowsMiddleVariantWhileOriginalLoads(t *testing.T) {
	h := newViewerHarness(t)
	s := h.store
	s.hold = make(chan struct{})
	h.viewer.Open(1, s.photos[14], nil)
	// The stage is 656×460: half of it is covered by the 800×600 "x" variant.
	h.until("the middle variant", func() bool {
		n, _ := h.viewer.mid.Stats()
		return n == 1 && h.viewer.mid.StatusFit(h.viewer.variant(s.photos[14], &s.photos[14].Media.Variants[1]), false, image.Pt(328, 230), false).Frame != nil
	})
	if wake, ok := h.router.WakeupTime(); !ok || wake.After(h.viewer.since.Add(viewerRingDelay)) {
		t.Fatalf("no redraw scheduled for when the ring is due: %v %v", wake, ok)
	}
	close(s.hold)
	h.until("the original", func() bool {
		return h.viewer.full.StatusFit(s.photos[14], false, image.Pt(656, 460), false).Frame != nil
	})
}

// The bar saves the original in the user's pictures and copies it as PNG;
// a protected photo offers neither.
func TestPhotoViewerSavesAndCopies(t *testing.T) {
	h := newViewerHarness(t)
	s := h.store
	h.viewer.Open(1, s.photos[3], nil)
	h.frame()
	h.viewer.save.Click()
	h.until("the saved notice", func() bool { return strings.HasPrefix(h.viewer.toast.Text(), "Photo saved: ") })
	path := strings.TrimPrefix(h.viewer.toast.Text(), "Photo saved: ")
	want, _ := s.Media(context.Background(), s.photos[3])
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) || filepath.Ext(path) != ".png" {
		t.Fatalf("saved %s: %v", path, err)
	}
	// Ctrl+C copies, as the button does.
	h.router.Queue(key.Event{Name: "C", Modifiers: key.ModShortcut, State: key.Press})
	h.until("the copied notice", func() bool { return h.viewer.toast.Text() == "Photo copied to clipboard." })
	h.frame()
	mime, data, ok := h.router.WriteClipboard()
	if !ok || mime != "image/png" {
		t.Fatalf("clipboard %q, %v", mime, ok)
	}
	if im, err := png.Decode(bytes.NewReader(data)); err != nil || im.Bounds().Size() != image.Pt(1600, 1200) {
		t.Fatalf("copied picture: %v", err)
	}
	if !canKeep(s.photos[3]) {
		t.Fatal("a photo may not be kept")
	}
	protected := s.photos[3]
	protected.NoForwards = true
	if canKeep(protected) {
		t.Fatal("a protected photo may be kept")
	}
}

// The header places the photo in the whole gallery, as Telegram tells its
// count, from the end the gallery is known to end at, as Telegram Desktop
// does; until an end is reached it says only that this is a photo.
func TestPhotoViewerPlacesPhotoInGallery(t *testing.T) {
	h := newViewerHarness(t)
	s := h.store
	// 200 photos: more than one page past either side of the middle.
	s.photos = nil
	for id := 10; id <= 2000; id += 10 {
		s.photos = append(s.photos, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(id)}, Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: fmt.Sprintf("p%d/w", id), Width: 160, Height: 120}})
	}
	place := func() string {
		items, _, _, _, _ := h.viewer.snapshot()
		n, amount, ok := h.viewer.place(items, indexOf(items, h.viewer.current))
		if !ok {
			return "unknown"
		}
		return fmt.Sprintf("%d of %d", n, amount)
	}

	// The newest photo: its side ends at once, and the older pages count
	// back from it.
	h.viewer.Open(1, s.photos[199], nil)
	h.until("the first pages", func() bool { return h.count() > 1 })
	h.until("the newer side to end", func() bool { _, _, exhausted, _, _ := h.viewer.snapshot(); return exhausted[1] })
	if got := place(); got != "200 of 200" {
		t.Fatalf("newest photo: %s", got)
	}
	h.key(key.NameLeftArrow)
	if got := place(); got != "199 of 200" {
		t.Fatalf("the one before it: %s", got)
	}

	// A photo in the middle, neither end reached: no place yet.
	h.viewer.Open(1, s.photos[99], nil)
	h.until("the first pages", func() bool { return h.count() == 121 })
	if got := place(); got != "unknown" {
		t.Fatalf("middle photo: %s", got)
	}
	// Going to the oldest loaded photo reads the older end, and places it.
	h.key(key.NameHome)
	h.until("the older end", func() bool { _, _, exhausted, _, _ := h.viewer.snapshot(); return exhausted[0] })
	h.key(key.NameHome)
	if got := place(); got != "1 of 200" {
		t.Fatalf("oldest photo: %s", got)
	}

	// Pages of the cache tell no total: only a gallery read to both ends is
	// counted.
	s.cached = true
	h.viewer.Open(1, s.photos[199], nil)
	h.until("the first pages", func() bool { return h.count() > 1 })
	if got := place(); got != "unknown" {
		t.Fatalf("cached newest photo: %s", got)
	}
	s.photos = s.photos[:30]
	h.viewer.Open(1, s.photos[29], nil)
	h.until("the whole gallery", func() bool { return h.count() == 30 })
	h.until("both ends", func() bool { _, _, exhausted, _, _ := h.viewer.snapshot(); return exhausted[0] && exhausted[1] })
	if got := place(); got != "30 of 30" {
		t.Fatalf("cached gallery read whole: %s", got)
	}
}

// A video without a thumbnail has an empty plate in the strip: it panicked
// there, and the window stayed on its panic screen.
func TestPhotoViewerStripShowsVideoWithoutThumbnail(t *testing.T) {
	h := newViewerHarness(t)
	s := h.store
	// The gallery has these three alone: pages loaded behind would move
	// the strip under the click.
	s.photos = s.photos[13:16]
	s.photos[2].Kind = model.MessageVideo
	h.viewer.Open(1, s.photos[1], s.photos)
	h.until("the gallery's ends", func() bool { _, _, exhausted, _, _ := h.viewer.snapshot(); return exhausted[0] && exhausted[1] })
	// Three thumbnails, centred: the third is at x 431…487.
	h.click(459, 558)
	if h.viewer.current != s.photos[2].Key.MessageID {
		t.Fatalf("the plate selected %d", h.viewer.current)
	}
}

// A viewer whose layout panicked closes in the frame after, for the window
// to show what is under it, instead of panicking again in every frame.
func TestPhotoViewerClosesAfterPanic(t *testing.T) {
	h := newViewerHarness(t)
	s := h.store
	h.viewer.Open(1, s.photos[14], s.photos[13:16])
	h.frame()
	if h.viewer.recoverFrame() {
		t.Fatal("a viewer drawn whole recovered")
	}
	// The thumbnails' state is written in every frame: without it, Layout
	// panics.
	h.viewer.thumbState = nil
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("no panic")
			}
		}()
		h.frame()
	}()
	if !h.viewer.recoverFrame() || h.viewer.open {
		t.Fatal("the viewer stayed open after its layout panicked")
	}
	h.viewer.thumbState = map[model.MessageID]*viewerThumbState{}
	h.viewer.Open(1, s.photos[14], s.photos[13:16])
	h.frame()
	h.frame()
}
