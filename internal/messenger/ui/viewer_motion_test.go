// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// motionStore is the demo's store that tells what it was asked for, and whose video is
// one that fails, unless a test gives it one.
type motionStore struct {
	*mockstore.Store
	mu    sync.Mutex
	asked map[string]int
}

func (s *motionStore) Media(ctx context.Context, m model.Message) ([]byte, error) {
	s.mu.Lock()
	if s.asked == nil {
		s.asked = map[string]int{}
	}
	s.asked[m.Media.ID]++
	s.mu.Unlock()
	if m.Media.ID == "demo/gifv" {
		return nil, errors.New("no video here")
	}
	return s.Store.Media(ctx, m)
}

func (s *motionStore) times(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked[id]
}

// motionViewer has a profile open, drawn with animations as animate says.
func motionViewer(t *testing.T) (*mediaViewer, *motionStore, func(animate bool)) {
	t.Helper()
	t.Chdir("../../..")
	store := &motionStore{Store: mockstore.New(time.Now(), 0)}
	store.ProfileVideos = true
	var images imageOps
	v := newMediaViewer(store, &images, func() {})
	t.Cleanup(v.Destroy)
	if !v.OpenProfile(2) {
		t.Fatal("the viewer did not open")
	}
	return v, store, func(animate bool) {
		gtx := sharedContext(new(op.Ops), image.Pt(800, 600))
		images.BeginFrame()
		v.Layout(gtx, localization.For("en"), animate)
		images.EndFrame()
	}
}

// The video is asked for with animations off too, as a GIF opened plays: on a machine
// drawing by the CPU they are off of themselves. What fails of it tells nothing.
func TestProfileVideoPlaysWithAnimationsOff(t *testing.T) {
	v, store, frame := motionViewer(t)
	item, ok := v.profiles.ProfilePhoto(2)
	if !ok || item.Media.Motion == nil {
		t.Fatalf("%+v", item.Media)
	}
	deadline := time.Now().Add(5 * time.Second)
	for store.times("demo/gifv") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the video was not asked for")
		}
		frame(false)
		time.Sleep(5 * time.Millisecond)
	}
	// It fails: the photo stays, with nothing told.
	for range 20 {
		frame(false)
		time.Sleep(5 * time.Millisecond)
	}
	if v.toast.Text() != "" {
		t.Fatalf("told %q", v.toast.Text())
	}
}

// In the editor it does not play: what is edited is the photo.
func TestProfileVideoStopsInTheEditor(t *testing.T) {
	v, _, _ := motionViewer(t)
	item, _ := v.profiles.ProfilePhoto(2)
	if _, ok := v.motionPlaying(item); !ok {
		t.Fatal("it does not play")
	}
	v.edit.open = true
	defer func() { v.edit.open = false }()
	if _, ok := v.motionPlaying(item); ok {
		t.Fatal("it plays in the editor")
	}
}

// The photo a profile opens with is known from the chat alone: the first page tells
// where it is and its video, and it goes there, staying as it is, already decoded,
// under a new list. The page's own first photo is not lost under it.
func TestFirstPagePlacesTheShownPhoto(t *testing.T) {
	key := func(i int) model.MessageKey { return model.MessageKey{ChatID: 2, MessageID: model.ProfilePhotoID(i)} }
	shown := model.Message{Kind: model.MessagePhoto, Key: key(0), Media: &model.MessageMedia{ID: "avatar/2/9/big"}}
	motion := &model.MessageMedia{ID: "photo/9/video/u"}
	page := model.GalleryPage{Current: model.ProfilePhotoID(2), Messages: []model.Message{
		{Kind: model.MessagePhoto, Key: key(0), Media: &model.MessageMedia{ID: "photo/7/x"}},
		{Kind: model.MessagePhoto, Key: key(1), Media: &model.MessageMedia{ID: "photo/8/x"}},
		{Kind: model.MessagePhoto, Key: key(2), Media: &model.MessageMedia{ID: "photo/9/x", Motion: motion}},
	}}
	before := []model.Message{shown}
	kept, add := placeShown(before, page)
	after := mergeMedia(galleryProfile, kept, add)
	var ids []string
	for _, m := range after {
		ids = append(ids, m.Media.ID)
	}
	if len(after) != 3 || ids[0] != "photo/7/x" || ids[1] != "photo/8/x" || ids[2] != "avatar/2/9/big" || after[2].Media.Motion != motion {
		t.Fatalf("%v", ids)
	}
	if before[0].Media.Motion != nil || page.Messages[2].Media.ID != "photo/9/x" {
		t.Fatal("changed a list a frame goes through")
	}
	// Where the page tells none, it stays first, and takes no video of another.
	page.Current = 0
	kept, add = placeShown(before, page)
	after = mergeMedia(galleryProfile, kept, add)
	if after[0].Media.ID != "avatar/2/9/big" || after[0].Media.Motion != nil {
		t.Fatalf("%+v", after[0].Media)
	}
}

// placedStore is the demo's store whose profiles show their third photo, as a user
// shows one of the list that is not its first.
type placedStore struct{ *mockstore.Store }

func (s placedStore) ProfilePhoto(chat int64) (model.Message, bool) {
	m, ok := s.Store.ProfilePhoto(chat)
	if ok {
		m.Media = &model.MessageMedia{ID: "demo/shown", MIMEType: "image/png", Width: 64, Height: 64}
	}
	return m, ok
}

func (s placedStore) ProfilePhotos(ctx context.Context, chat int64, offset string, limit int) (model.GalleryPage, error) {
	page, err := s.Store.ProfilePhotos(ctx, chat, offset, limit)
	if offset == "" {
		page.Current = model.ProfilePhotoID(2)
	}
	return page, err
}

// The viewer opens a profile on the photo it shows now, where it is among the others,
// as Telegram Desktop does, not first.
func TestProfileOpensOnTheShownPhoto(t *testing.T) {
	t.Chdir("../../..")
	var images imageOps
	v := newMediaViewer(placedStore{mockstore.New(time.Now(), 0)}, &images, func() {})
	defer v.Destroy()
	if !v.OpenProfile(2) {
		t.Fatal("the viewer did not open")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		gtx := sharedContext(new(op.Ops), image.Pt(800, 600))
		images.BeginFrame()
		v.Layout(gtx, localization.For("en"), false)
		images.EndFrame()
		items, _, _, _, _ := v.snapshot()
		if len(items) > 3 {
			i := indexOf(items, v.current)
			if v.current != model.ProfilePhotoID(2) || items[i].Media.ID != "demo/shown" {
				t.Fatalf("on %v, %s", v.current, items[i].Media.ID)
			}
			if items[0].Media.ID == "demo/shown" {
				t.Fatal("the first photo is lost under it")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no page")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Saved, one that plays is the video, in an MP4; copied, the picture.
func TestKeepingAProfileVideo(t *testing.T) {
	motion := &model.MessageMedia{ID: "photo/9/video/u", MIMEType: "video/mp4"}
	m := model.Message{Kind: model.MessagePhoto, Key: model.MessageKey{ChatID: 2, MessageID: model.ProfilePhotoID(0)}, Media: &model.MessageMedia{ID: "photo/9/x", Motion: motion}}
	if got := keptMedia(m, false); got.Media.ID != motion.ID {
		t.Fatalf("saved %s", got.Media.ID)
	}
	if got := keptMedia(m, true); got.Media.ID != "photo/9/x" {
		t.Fatalf("copied %s", got.Media.ID)
	}
	plain := m.WithMedia(&model.MessageMedia{ID: "photo/8/x"})
	if got := keptMedia(plain, false); got.Media.ID != "photo/8/x" {
		t.Fatalf("saved %s", got.Media.ID)
	}
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", os.Getenv("USERPROFILE"))
	data := append([]byte{0, 0, 0, 0x18}, []byte("ftypmp42\x00\x00\x00\x00mp42isom")...)
	path, err := savePhoto(data, m)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if filepath.Ext(path) != ".mp4" {
		t.Fatalf("saved as %s", path)
	}
}

// What saving asks the store for is the video, and it is written as one.
func TestKeepPhotoSavesTheVideo(t *testing.T) {
	v, store, _ := motionViewer(t)
	item, _ := v.profiles.ProfilePhoto(2)
	v.keepPhoto(item, false, localization.For("en"))
	deadline := time.Now().Add(5 * time.Second)
	for store.times("demo/gifv") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("saving did not ask for the video")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if store.times("demo/portrait") != 0 {
		t.Fatal("saving asked for the photo")
	}
}

// With the decoder at hand its pictures go over the photo, and a click pauses them.
func TestProfileVideoPlays(t *testing.T) {
	roundDecoders(t)
	t.Chdir("../../..")
	var images imageOps
	store := mockstore.New(time.Now(), 0)
	store.ProfileVideos = true
	v := newMediaViewer(store, &images, func() {})
	defer v.Destroy()
	if !v.OpenProfile(2) {
		t.Fatal("the viewer did not open")
	}
	item, _ := v.profiles.ProfilePhoto(2)
	motion, _ := motionOf(item)
	frame := func() {
		gtx := sharedContext(new(op.Ops), image.Pt(800, 600))
		images.BeginFrame()
		v.Layout(gtx, localization.For("en"), true)
		images.EndFrame()
	}
	deadline := time.Now().Add(30 * time.Second)
	// Its pictures go on: asked of the manager at another size here, it would start over.
	for v.full.GIFPosition(motion) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no picture of the video")
		}
		frame()
		time.Sleep(10 * time.Millisecond)
	}
	frame()
	if v.full.GIFPaused(motion) {
		t.Fatal("paused from the start")
	}
	for v.shownMotion == nil {
		if time.Now().After(deadline) {
			t.Fatal("the video's picture is not what is drawn")
		}
		frame()
		time.Sleep(10 * time.Millisecond)
	}
	v.picture.Click()
	frame()
	frame()
	if !v.full.GIFPaused(motion) {
		t.Fatal("a click did not pause it")
	}
}
