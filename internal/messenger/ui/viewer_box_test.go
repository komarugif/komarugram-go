// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
	"komarugram/pkg/audio"
)

// boxFixture is the files of a box: photos, a video and a GIF, which open, a text
// and a photo not read yet, which do not.
func boxFixture(t *testing.T) []*boxFile {
	t.Helper()
	dir := t.TempDir()
	thumb := gradientImage(64, 36, 1)
	file := func(path string, kind sendfiles.Kind, mime string) *boxFile {
		return &boxFile{File: sendfiles.File{Path: path, Name: filepath.Base(path), Size: 100, Kind: kind, MIME: mime, Width: 64, Height: 36}, ready: true, thumb: thumb}
	}
	return []*boxFile{
		file(pictureFile(t, dir, "a.png", 64, 36, 1), sendfiles.KindPhoto, "image/png"),
		file(writeTemp(t, dir, "notes.txt", []byte("notes")), sendfiles.KindFile, "text/plain"),
		file(pictureFile(t, dir, "b.jpg", 64, 36, 2), sendfiles.KindPhoto, "image/jpeg"),
		{File: sendfiles.File{Path: "unread.png", Name: "unread.png", Kind: sendfiles.KindPhoto}},
		file(writeTemp(t, dir, "c.mp4", []byte("not much of a video")), sendfiles.KindVideo, "video/mp4"),
		file(writeTemp(t, dir, "d.gif", gifFile(t)), sendfiles.KindAnimation, "image/gif"),
	}
}

// What opens is the photos, the videos and the GIFs read by now, in the box's order, and
// no message of any chat; a video shows the thumbnail the box has.
func TestBoxGalleryHoldsWhatOpens(t *testing.T) {
	files := boxFixture(t)
	files[4].shown, files[4].long = image.Pt(1920, 1080), time.Minute
	media, items, current := boxGallery(mockstore.New(time.Now(), 0), 2, files, files[2])
	if len(items) != 4 || items[0].Media.FileName != "a.png" || items[1].Media.FileName != "b.jpg" || items[2].Media.FileName != "c.mp4" || items[3].Kind != model.MessageGIF {
		t.Fatalf("items %+v", items)
	}
	if current.Key != items[1].Key {
		t.Fatalf("current %+v", current.Key)
	}
	if v := items[2]; v.Kind != model.MessageVideo || v.Media.Width != 1920 || v.Media.Height != 1080 || v.Media.Duration != time.Minute || v.Media.Thumbnail == nil {
		t.Fatalf("the video %+v", v.Media)
	}
	data, err := media.Media(context.Background(), items[2].WithMedia(items[2].Media.Thumbnail))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("the thumbnail: %v", err)
	}
	if _, err := media.Media(context.Background(), model.Message{Media: &model.MessageMedia{ID: "document/1"}}); err == nil {
		t.Fatal("it read what is no file of the box")
	}
	r, size, done, err := media.MediaStream(context.Background(), items[2])
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	head := make([]byte, 3)
	if _, err := r.ReadAt(head, 0); err != nil || string(head) != "not" || size != 100 {
		t.Fatalf("the stream: %q of %d, %v", head, size, err)
	}
}

// The viewer of the box knows of no chat: however much its store may do, it
// pages nothing, forwards nothing and saves nothing. It copies, and it goes
// through the files it was given, which it counts.
func TestBoxViewerIsIsolated(t *testing.T) {
	files := boxFixture(t)
	store := mockstore.New(time.Now(), 0)
	media, items, current := boxGallery(store, 2, files, files[0])
	var images imageOps
	v := newMediaViewer(media, &images, func() {})
	defer v.Destroy()
	v.share.chats = store.Chats
	v.popout = nil
	if v.photos != nil || v.gifs != nil || v.profiles != nil || v.stories != nil || v.chatStories != nil || v.shared != nil || v.share.forwarder != nil || v.gif.maker != nil {
		t.Fatal("the viewer of the box reaches the store's galleries or forwards")
	}
	v.openList(2, current, mediaList{items: items, ended: [2]bool{true, true}, gallery: galleryBox})
	photo := items[0]
	if v.canForward(photo) || v.canMakeGIF(items[2]) {
		t.Fatal("it offers to forward or make a GIF")
	}
	if v.canSave(photo) || !canKeep(photo) {
		t.Fatal("it should copy and not save")
	}
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(800, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineDark()))
	l := localization.For("en")
	frame := func() {
		gtx.Ops.Reset()
		images.BeginFrame()
		v.Layout(gtx, l, false)
		images.EndFrame()
	}
	frame()
	got, _, exhausted, _, _ := v.snapshot()
	if len(got) != 4 || exhausted != [2]bool{true, true} {
		t.Fatalf("%d items, exhausted %v", len(got), exhausted)
	}
	if want := l.Format("viewer.file_position", map[string]string{"file": l.T("viewer.attachment"), "n": "1", "amount": "4"}); v.title(photo, got, 0, l) != want {
		t.Fatalf("title %q", v.title(photo, got, 0, l))
	}
	// A click on the save button, or its key, keeps nothing either.
	v.save.Click()
	frame()
	if v.kept != nil {
		t.Fatal("it saved")
	}
	// The picture comes from the disk, and what follows it is the next file.
	deadline := time.Now().Add(10 * time.Second)
	for v.full.StatusFit(photo, false, image.Pt(100, 100), false).Frame == nil {
		if time.Now().After(deadline) {
			t.Fatal("the picture did not come")
		}
		frame()
		time.Sleep(5 * time.Millisecond)
	}
	v.show(got, 2, true)
	frame()
	if v.drawn != items[2].Key.MessageID {
		t.Fatalf("drew %d", v.drawn)
	}
	// A GIF plays from its file too.
	v.show(got, 3, true)
	for v.full.StatusFit(items[3], true, image.Pt(100, 100), false).Frame == nil {
		if time.Now().After(deadline) {
			t.Fatal("the GIF did not come")
		}
		frame()
		time.Sleep(5 * time.Millisecond)
	}
}

// A video of the box plays in the viewer, from the disk, by FFmpeg.
func TestBoxVideoPlaysFromTheDisk(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip(err)
	}
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=s=64x36:d=1", "-f", "lavfi", "-i", "sine=d=1", "-shortest", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	info, err := sendfiles.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &boxFile{File: info, ready: true, shown: image.Pt(64, 36), long: time.Second}
	media, items, current := boxGallery(mockstore.New(time.Now(), 0), 2, []*boxFile{f}, f)
	var images imageOps
	v := newMediaViewer(media, &images, func() {})
	defer v.Destroy()
	v.video.play = func(src audio.Source) (audioPlayback, error) {
		return &silentPlayback{src: src, playing: true}, nil
	}
	v.openList(2, current, mediaList{items: items, ended: [2]bool{true, true}, gallery: galleryBox})
	v.video.open(media, current, videoFFmpeg, ffmpeg, -1)
	deadline := time.Now().Add(20 * time.Second)
	for !v.video.state(current).playing {
		if err := v.video.takeErr(); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("it did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// boxHarness draws the page with the viewer of the box over it, as the window
// does.
type boxHarness struct {
	*menuHarness
	viewer *mediaViewer
	closed func()
}

func (h *boxHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(500, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	l := localization.For("en")
	h.page.images.BeginFrame()
	h.page.media.BeginFrame()
	h.page.Layout(gtx, model.Chat{ID: 1, Title: "Chat", Kind: model.KindGroup}, l, false)
	h.page.layoutDialogs(gtx, l)
	h.page.media.EndFrame()
	if h.viewer != nil {
		h.viewer.Layout(gtx, l, false)
		if !h.viewer.open {
			h.viewer.Destroy()
			h.viewer = nil
			h.closed()
		}
	}
	h.page.images.EndFrame()
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(16 * time.Millisecond)
}

func newBoxHarness(t *testing.T, kinds ...string) *boxHarness {
	h := &boxHarness{menuHarness: newMenuHarness(t, nil)}
	c := h.page.composer
	h.page.openBoxFiles = func(media *boxMedia, chat int64, current model.Message, items []model.Message, closed func()) {
		h.viewer = newMediaViewer(media, h.page.images, func() {})
		h.viewer.openList(chat, current, mediaList{items: items, ended: [2]bool{true, true}, gallery: galleryBox})
		h.closed = closed
	}
	t.Cleanup(func() {
		if h.viewer != nil {
			h.viewer.Destroy()
		}
	})
	c.files.addPaths(c, boxPaths(t, kinds...), false)
	settleBox(t, h.menuHarness)
	return h
}

// pointOver finds the point of the window where hovered says the pointer is.
func (h *boxHarness) pointOver(t *testing.T, hovered func() bool) f32.Point {
	t.Helper()
	for y := float32(10); y < 700; y += 16 {
		for x := float32(10); x < 500; x += 16 {
			p := f32.Pt(x, y)
			h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p})
			h.frame()
			if hovered() {
				return p
			}
		}
	}
	t.Fatal("nothing there")
	return f32.Point{}
}

func (h *boxHarness) click(p f32.Point) {
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: p})
		h.frame()
	}
}

// A click on a picture of the box opens the viewer on it, among the box's, and
// the button that takes it out still takes it out.
func TestBoxOpensThePictureClicked(t *testing.T) {
	h := newBoxHarness(t, "photo", "photo", "photo")
	c := h.page.composer
	h.frame()
	second := c.files.files[1]
	h.click(h.pointOver(t, second.open.Hovered))
	if h.viewer == nil {
		t.Fatal("the viewer did not open")
	}
	items, _, _, _, _ := h.viewer.snapshot()
	if len(items) != 3 || h.viewer.current != items[1].Key.MessageID || h.viewer.gallery != galleryBox {
		t.Fatalf("%d items, on %d", len(items), h.viewer.current)
	}
	h.viewer.Close()
	h.frame()

	third := c.files.files[2]
	h.click(h.pointOver(t, third.remove.click.Hovered))
	if h.viewer != nil {
		t.Fatal("the button that takes it out opened it")
	}
	if len(c.files.files) != 2 {
		t.Fatalf("%d files", len(c.files.files))
	}
}

// A file with no picture for the viewer does not open.
func TestBoxLeavesFilesClosed(t *testing.T) {
	h := newBoxHarness(t, "file", "photo")
	c := h.page.composer
	c.files.files[0].open.Click()
	h.frame()
	if h.viewer != nil {
		t.Fatal("a text opened")
	}
}

// Escape closes the viewer alone, and the caption goes on being typed.
func TestBoxViewerClosesAloneOnEscape(t *testing.T) {
	h := newBoxHarness(t, "photo", "photo")
	c := h.page.composer
	h.frame()
	c.files.files[0].open.Click()
	h.frame()
	if h.viewer == nil {
		t.Fatal("the viewer did not open")
	}
	h.frame()
	h.frame()
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame()
	h.frame()
	if h.viewer != nil {
		t.Fatal("the viewer stayed")
	}
	if !c.files.Shown() || c.files.modal.closing {
		t.Fatal("the box closed with it")
	}
	h.frame()
	h.frame()
	if !h.router.Source().Focused(&c.files.caption) {
		t.Fatal("the caption lost the focus")
	}
}
