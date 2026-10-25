// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"slices"
	"sync"
	"testing"

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
