// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// wholeMediaStore tells which media were asked for whole, and has none.
type wholeMediaStore struct {
	*menuStore
	mu    sync.Mutex
	asked []string
}

func (s *wholeMediaStore) Media(_ context.Context, m model.Message) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, m.Media.ID)
	return nil, errors.New("media exceeds 64 MiB limit")
}

func (s *wholeMediaStore) MediaProgress(ctx context.Context, m model.Message, _ func(int64, int64)) ([]byte, error) {
	return s.Media(ctx, m)
}

// An album of files, as archives sent together, is a list of files, as
// Telegram Desktop shows it: none is downloaded to be drawn as a picture.
func TestFileAlbumIsAList(t *testing.T) {
	var store *wholeMediaStore
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		for i, name := range []string{"KomaruGram-windows-x64.zip", "KomaruGram.tar.zst"} {
			m := &messages[17+i]
			m.Kind, m.Text, m.GroupedID = model.MessageFile, "", 7
			m.Media = &model.MessageMedia{ID: "document/" + name, MIMEType: "application/zip", Size: 82886219, FileName: name}
		}
	}, func(s *menuStore) model.ConversationStore {
		store = &wholeMediaStore{menuStore: s}
		return store
	})
	h.frames(5)
	time.Sleep(100 * time.Millisecond)
	h.frames(5)
	album := false
	for _, m := range h.page.messages {
		album = album || len(m.Attachments) == 2
	}
	if !album {
		t.Fatal("the files are not an album")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.asked) != 0 || h.page.toast.Text() != "" {
		t.Fatalf("asked for %q whole, toast %q", store.asked, h.page.toast.Text())
	}
}

// A GIF too large to be read whole shows its thumbnail, to be played as a
// video: it is not asked for whole.
func TestLargeGIFIsNotReadWhole(t *testing.T) {
	var store *wholeMediaStore
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		m := &messages[1]
		m.Kind, m.Text = model.MessageGIF, ""
		m.Media = &model.MessageMedia{ID: "document/gif", MIMEType: "video/mp4", Width: 640, Height: 360, Size: model.MaxMediaBytes + 1,
			Thumbnail: &model.MessageMedia{ID: "document/gif/thumb", MIMEType: "image/jpeg", Width: 320, Height: 180}}
	}, func(s *menuStore) model.ConversationStore {
		store = &wholeMediaStore{menuStore: s}
		return store
	})
	h.frames(5)
	time.Sleep(100 * time.Millisecond)
	h.frames(5)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.asked) != 1 || store.asked[0] != "document/gif/thumb" {
		t.Fatalf("asked for %q", store.asked)
	}
}

// With animations off, a GIF plays while the pointer is on it, as a
// sticker does; a photo or a video does not.
func TestTileAnimatesOnHover(t *testing.T) {
	for _, kind := range []model.MessageKind{model.MessageGIF, model.MessageSticker} {
		if !tileAnimates(kind, false, true) || tileAnimates(kind, false, false) || !tileAnimates(kind, true, false) {
			t.Errorf("kind %v", kind)
		}
	}
	for _, kind := range []model.MessageKind{model.MessagePhoto, model.MessageVideo} {
		if tileAnimates(kind, false, true) {
			t.Errorf("kind %v plays on hover", kind)
		}
	}
}
