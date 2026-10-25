// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"slices"
	"sync"
	"testing"
	"time"

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

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// storyGallery is a gallery whose peers 6, 7 and 8 have one, three and
// two stories, the gallery's photos, given once release is closed; it
// records what the viewer tells of them.
type storyGallery struct {
	*galleryStore
	release chan struct{}
	mu      sync.Mutex
	told    []string
	peers   []model.StoryPeer
}

func newStoryGallery() *storyGallery {
	return &storyGallery{galleryStore: newGalleryStore(), release: make(chan struct{})}
}

func (s *storyGallery) story(n int) model.Message { return s.peerStory(7, n) }

func (s *storyGallery) peerStory(peer int64, n int) model.Message {
	m := s.photos[int(peer)*3+n]
	m.Key = model.MessageKey{ChatID: peer, MessageID: model.StoryMessage(n)}
	m.Story = &model.Story{Likes: n}
	return m
}

func (s *storyGallery) record(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.told = append(s.told, fmt.Sprintf(format, args...))
}

func (s *storyGallery) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.told)
}

func (s *storyGallery) StoryPeers() []model.StoryPeer { return s.peers }

func (s *storyGallery) PeerStories(ctx context.Context, peer int64) ([]model.Message, error) {
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Newest first, as nothing promises: the viewer orders them.
	switch peer {
	case 6:
		return []model.Message{s.peerStory(6, 1)}, nil
	case 7:
		return []model.Message{s.story(3), s.story(1), s.story(2)}, nil
	case 8:
		return []model.Message{s.peerStory(8, 2), s.peerStory(8, 1)}, nil
	}
	return nil, nil
}

func (s *storyGallery) ReadStory(peer int64, id model.MessageID) {
	s.record("read %d %d", peer, model.StoryID(id))
}

func (s *storyGallery) LikeStory(_ context.Context, peer int64, id model.MessageID, like bool) error {
	s.record("like %d %d %v", peer, model.StoryID(id), like)
	return nil
}

func (s *storyGallery) StoryNow(peer int64, id model.MessageID) (model.Message, bool) {
	return s.peerStory(peer, model.StoryID(id)), true
}

func (s *storyGallery) ShareStory(_ context.Context, peer int64, id model.MessageID, to int64) error {
	s.record("share %d %d to %d", peer, model.StoryID(id), to)
	return nil
}

func (s *storyGallery) ForwardMessages(_ context.Context, from int64, ids []model.MessageID, to int64) error {
	s.record("forward %d %v to %d", from, ids, to)
	return nil
}

// The stories of the whole row open in the viewer as any media, one
// gallery: before Telegram has told them, waiting, then each peer's the
// oldest first, in the order of the row, those beside asked for as the
// strip comes near them; the arrows go on from one's to the next one's.
// Each is told seen as it is shown, and its heart and its forwarding go
// to the story by its own id, whatever the viewer keys it by.
func TestViewerStories(t *testing.T) {
	h := newViewerHarness(t)
	src := newStoryGallery()
	src.peers = []model.StoryPeer{{ID: 6}, {ID: 7}, {ID: 8}}
	h.viewer = newMediaViewer(src, &h.images, func() {
		select {
		case h.changed <- struct{}{}:
		default:
		}
	})
	h.viewer.share.chats = func() []model.Chat { return []model.Chat{{ID: 9, Kind: model.KindUser, Title: "Vera"}} }
	t.Cleanup(h.viewer.Destroy)
	v := h.viewer
	if !v.OpenStories(7, model.StoryMessage(2)) {
		t.Fatal("no stories")
	}
	for range 5 {
		h.frame()
	}
	if !v.open || len(src.recorded()) != 0 {
		t.Fatalf("waiting: open %v, told %q", v.open, src.recorded())
	}
	close(src.release)
	h.until("the row", func() bool { items, _, _, _, _ := v.snapshot(); return len(items) == 6 })
	items, _, _, _, _ := v.snapshot()
	var order []string
	for _, m := range items {
		m = v.realStory(m)
		order = append(order, fmt.Sprintf("%d:%d", m.Key.ChatID, model.StoryID(m.Key.MessageID)))
	}
	if want := []string{"6:1", "7:1", "7:2", "7:3", "8:1", "8:2"}; !slices.Equal(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	if cur := v.realStory(items[indexOf(items, v.current)]); cur.Key.ChatID != 7 || model.StoryID(cur.Key.MessageID) != 2 {
		t.Fatalf("opened on %+v", cur.Key)
	}
	for range 2 {
		h.router.Queue(key.Event{Name: key.NameRightArrow, State: key.Press})
		h.frame()
		h.frame()
	}
	i := indexOf(items, v.current)
	if cur := v.realStory(items[i]); cur.Key.ChatID != 8 || model.StoryID(cur.Key.MessageID) != 1 {
		t.Fatalf("next of the last of 7 is %+v", cur.Key)
	}
	if n, amount, _ := v.place(items, i); n != 1 || amount != 2 {
		t.Fatalf("placed %d of %d", n, amount)
	}
	v.share.like.Click()
	h.frame()
	v.share.forward.Click()
	h.frame()
	if !v.share.picker.modal.Shown() {
		t.Fatal("the picker did not open")
	}
	h.frame()
	v.share.picker.rows[9].click.Click()
	h.frame()
	want := []string{"read 7 2", "read 7 3", "read 8 1", "like 8 1 true", "share 8 1 to 9"}
	h.until("told", func() bool { return len(src.recorded()) >= len(want) })
	if told := src.recorded(); !slices.Equal(told, want) {
		t.Fatalf("told %q, want %q", told, want)
	}
}

// What the viewer forwards: a message of Telegram's, or a story, unless it
// protects its content; not a profile photo, a message on its way, or a
// picture shown alone.
func TestViewerCanForward(t *testing.T) {
	h := newViewerHarness(t)
	src := newStoryGallery()
	v := newMediaViewer(src, &h.images, func() {})
	t.Cleanup(v.Destroy)
	v.share.chats = func() []model.Chat { return nil }
	msg := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 10}, Kind: model.MessagePhoto}
	protected := msg
	protected.NoForwards = true
	sending := msg
	sending.Key.MessageID, sending.Sending = -3, true
	profile := msg
	profile.Key.MessageID = model.ProfilePhotoID(0)
	story := src.story(1)
	for _, c := range []struct {
		m    model.Message
		want bool
	}{{msg, true}, {protected, false}, {sending, false}, {profile, false}, {story, true}} {
		if got := v.canForward(c.m); got != c.want {
			t.Errorf("%+v: %v", c.m.Key, got)
		}
	}
	v.alone = true
	if v.canForward(msg) {
		t.Error("a picture alone is forwarded")
	}
	v.alone, v.share.chats = false, nil
	if v.canForward(msg) {
		t.Error("forwarded with no chats to forward to")
	}
}

// chatListHarness lays out a chat list, as the window does, with input.
type chatListHarness struct {
	router input.Router
	list   *chatList
	sec    section
	chats  []model.Chat
}

func (h *chatListHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(360, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	catalog := localization.For("en")
	h.list.Update(gtx, h.sec, catalog)
	h.list.Layout(gtx, h.sec, nil, h.chats, 0, false, catalog)
	h.router.Frame(gtx.Ops)
}

func (h *chatListHarness) click(at f32.Point) {
	h.router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: pointer.ButtonPrimary},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at},
	)
	h.frame()
}

// The row of stories is the first of all chats, and goes up with them; a
// click on someone in it opens theirs. The chats in view stay where they
// are when it comes; a folder has none.
func TestStoriesRowInList(t *testing.T) {
	src := newStoryGallery()
	h := &chatListHarness{list: newChatList(), sec: section{kind: sectionAll}}
	for id := int64(1); id <= 40; id++ {
		h.chats = append(h.chats, model.Chat{ID: id, Kind: model.KindUser, Title: fmt.Sprint("Chat ", id)})
	}
	h.list.stories = src
	var opened []int64
	h.list.openStories = func(p model.StoryPeer) { opened = append(opened, p.ID) }
	h.frame()
	if h.list.storiesShown {
		t.Fatal("a row without stories")
	}
	h.list.list.Position.First = 5
	src.peers = []model.StoryPeer{{ID: 6, Self: true, Count: 1, Unread: 1}, {ID: 7, Title: "Anna", Count: 3, Unread: 2}}
	h.frame()
	if !h.list.storiesShown || h.list.list.Position.First != 6 {
		t.Fatalf("shown %v, first %d", h.list.storiesShown, h.list.list.Position.First)
	}
	h.list.list.Position = layout.Position{}
	h.frame()
	// Anna, the second, at the list's top.
	at := float32(listTopGap) + 40
	h.click(f32.Pt(4+72+36, at))
	h.frame()
	if !slices.Equal(opened, []int64{7}) {
		t.Fatalf("opened %v", opened)
	}
	h.sec = section{kind: sectionFolder, folder: 1}
	h.frame()
	if h.list.storiesShown {
		t.Fatal("a folder has the row")
	}
}
