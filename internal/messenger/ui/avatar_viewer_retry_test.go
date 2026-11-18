// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// failingProfileGallery fails the first pages it is asked for, as Telegram
// does with FLOOD_WAIT.
type failingProfileGallery struct {
	*mockstore.Store
	fail  int32
	asked *atomic.Int32
}

func (s failingProfileGallery) ProfilePhotos(ctx context.Context, chat int64, offset string, limit int) (model.GalleryPage, error) {
	if s.asked.Add(1) <= s.fail {
		return model.GalleryPage{}, errors.New("FLOOD_WAIT_30")
	}
	return s.Store.ProfilePhotos(ctx, chat, offset, limit)
}

// A page of a profile's photos that Telegram refuses is asked for again
// later: the viewer does not stay with the one photo it opened with.
func TestProfileGalleryRetriesAfterFailure(t *testing.T) {
	old := profileRetryStep
	profileRetryStep = 5 * time.Millisecond
	defer func() { profileRetryStep = old }()
	asked := new(atomic.Int32)
	store := failingProfileGallery{Store: mockstore.New(time.Now(), 0), fail: 2, asked: asked}
	v := newMediaViewer(store, new(imageOps), func() {})
	defer v.Destroy()
	if !v.OpenProfile(2) {
		t.Fatal("profile did not open")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		// The frames ask again when they are drawn, as the strip nears its end.
		items, loading, exhausted, ctx, session := v.snapshot()
		if len(items) > 1 {
			break
		}
		if exhausted[1] {
			t.Fatal("a failed page ended the strip for good")
		}
		if !loading[1] {
			v.fetch(ctx, session, 1, items[len(items)-1].Key.MessageID)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no more photos after %d requests", asked.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if n := asked.Load(); n != 3 {
		t.Fatalf("asked %d times for 2 failures and a page", n)
	}
}
