// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"slices"
	"sync"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// sectionGallery is a gallery whose photos at ids divisible by 20 are
// videos, and whose info's sections page them, recording what they are
// asked for.
type sectionGallery struct {
	*galleryStore
	mu    sync.Mutex
	asked []model.SharedKind
}

func newSectionGallery() *sectionGallery {
	s := &sectionGallery{galleryStore: newGalleryStore()}
	for i := range s.photos {
		if s.photos[i].Key.MessageID%20 == 0 {
			s.photos[i].Kind = model.MessageVideo
		}
	}
	return s
}

func (s *sectionGallery) SharedCounts(context.Context, int64) (map[model.SharedKind]int, error) {
	return nil, nil
}

// SharedMedia pages the section of kind, before before, newest first.
func (s *sectionGallery) SharedMedia(_ context.Context, chat int64, kind model.SharedKind, before model.MessageID, limit int) (model.SharedPage, error) {
	s.mu.Lock()
	s.asked = append(s.asked, kind)
	s.mu.Unlock()
	want := model.MessagePhoto
	if kind == model.SharedVideos {
		want = model.MessageVideo
	}
	var page model.SharedPage
	for i := len(s.photos) - 1; i >= 0; i-- {
		m := s.photos[i]
		if m.Kind == want && (before == 0 || m.Key.MessageID < before) && len(page.Messages) < limit {
			page.Messages = append(page.Messages, m)
		}
	}
	return page, nil
}

// A photo or a video opened from a section of the info goes through that
// section's kind alone, and pages it; from the chat, through both.
func TestViewerSharedSection(t *testing.T) {
	h := newViewerHarness(t)
	src := newSectionGallery()
	h.viewer = newMediaViewer(src, &h.images, func() {
		select {
		case h.changed <- struct{}{}:
		default:
		}
	})
	t.Cleanup(h.viewer.Destroy)
	v := h.viewer
	// The section has loaded its latest: videos 300, 280 and 260.
	var known []model.Message
	for _, m := range src.photos[len(src.photos)-6:] {
		known = append(known, m)
	}
	video := src.photos[len(src.photos)-1]
	v.OpenShared(1, video, known, model.SharedVideos, -1)
	if v.autoplay != video.Key.MessageID {
		t.Fatalf("to play %d", v.autoplay)
	}
	h.until("older videos", func() bool { items, _, _, _, _ := v.snapshot(); return len(items) == 15 })
	items, _, _, _, _ := v.snapshot()
	for _, m := range items {
		if m.Kind != model.MessageVideo {
			t.Fatalf("the videos have photo %d", m.Key.MessageID)
		}
	}
	src.mu.Lock()
	asked := slices.Clone(src.asked)
	src.mu.Unlock()
	if len(asked) == 0 || slices.ContainsFunc(asked, func(k model.SharedKind) bool { return k != model.SharedVideos }) {
		t.Fatalf("asked for %v", asked)
	}
	photo := src.photos[len(src.photos)-2]
	v.OpenShared(1, photo, known, model.SharedPhotos, -1)
	h.until("older photos", func() bool { items, _, _, _, _ := v.snapshot(); return len(items) == 15 })
	items, _, _, _, _ = v.snapshot()
	for _, m := range items {
		if m.Kind != model.MessagePhoto {
			t.Fatalf("the photos have video %d", m.Key.MessageID)
		}
	}
	// From the chat, both.
	v.Open(1, photo, known)
	h.frame()
	items, _, _, _, _ = v.snapshot()
	kinds := map[model.MessageKind]bool{}
	for _, m := range items {
		kinds[m.Kind] = true
	}
	if !kinds[model.MessagePhoto] || !kinds[model.MessageVideo] {
		t.Fatalf("the chat's have %v", kinds)
	}
}

// The bar words where an item is by what the gallery goes through: the
// chat's photos and videos together are attachments, a section's videos
// videos, its photos photos, GIFs GIFs, and stories stories.
func TestViewerTitle(t *testing.T) {
	h := newViewerHarness(t)
	v := h.viewer
	l := localization.For("en")
	photo := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{}}
	video := model.Message{Kind: model.MessageVideo, Media: &model.MessageMedia{}}
	gif := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{}}
	story := photo
	story.StoryOf = &model.StoryRef{}
	for _, c := range []struct {
		gallery viewerGallery
		m       model.Message
		want    string
	}{
		{galleryPhotos, video, "Attachment 2 of 3"},
		{galleryPhotos, photo, "Attachment 2 of 3"},
		{galleryShared, video, "Video 2 of 3"},
		{galleryShared, photo, "Photo 2 of 3"},
		{galleryProfile, photo, "Photo 2 of 3"},
		{galleryGIFs, gif, "GIF 2 of 3"},
		{galleryChatStories, story, "Story 2 of 3"},
	} {
		v.mu.Lock()
		v.gallery, v.total, v.ended = c.gallery, 0, [2]bool{true, true}
		v.mu.Unlock()
		items := []model.Message{c.m, c.m, c.m}
		if got := v.title(c.m, items, 1, l); got != c.want {
			t.Errorf("%v: %q, want %q", c.gallery, got, c.want)
		}
	}
	// Where it is not known, what it is.
	v.mu.Lock()
	v.gallery, v.ended = galleryPhotos, [2]bool{}
	v.mu.Unlock()
	if got := v.title(video, []model.Message{video}, 0, l); got != "Video" {
		t.Errorf("unplaced: %q", got)
	}
}
