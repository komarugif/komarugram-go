// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"sync"
	"testing"
	"time"

	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// avatarStore is the demo's, telling the photos deleted and made main.
type avatarStore struct {
	*mockstore.Store
	mu            sync.Mutex
	deleted, main []model.MessageID
}

func (s *avatarStore) DeleteProfilePhoto(ctx context.Context, m model.Message) error {
	s.mu.Lock()
	s.deleted = append(s.deleted, m.Key.MessageID)
	s.mu.Unlock()
	return s.Store.DeleteProfilePhoto(ctx, m)
}

func (s *avatarStore) MakeProfilePhotoMain(ctx context.Context, m model.Message) error {
	s.mu.Lock()
	s.main = append(s.main, m.Key.MessageID)
	s.mu.Unlock()
	return s.Store.MakeProfilePhotoMain(ctx, m)
}

func (s *avatarStore) calls() (deleted, main int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleted), len(s.main)
}

// A group's current photo is deleted only once the dialog is answered, and another
// one made main at once; the viewer closes on either, and the window tells it.
func TestViewerAvatarButtons(t *testing.T) {
	store := &avatarStore{Store: mockstore.New(time.Now(), 0)}
	var images imageOps
	v := newMediaViewer(store, &images, func() {})
	defer v.Destroy()
	var told string
	v.told = func(text string) { told = text }
	frame := func() {
		gtx := sharedContext(new(op.Ops), image.Pt(800, 600))
		images.BeginFrame()
		v.Layout(gtx, localization.For("en"), false)
		images.EndFrame()
	}
	open := func() []model.Message {
		t.Helper()
		told = ""
		if !v.OpenProfile(3) {
			t.Fatal("the viewer did not open")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			frame()
			items, _, _, _, _ := v.snapshot()
			if len(items) == 3 {
				return items
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d photos", len(items))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("waiting for %s", what)
			}
			frame()
			time.Sleep(5 * time.Millisecond)
		}
	}

	items := open()
	current := items[0]
	if !v.canDeleteAvatar(current) || v.canMakeAvatarMain(current) {
		t.Fatal("the current photo: delete it, and it is the main one already")
	}
	v.avatar.remove.Click()
	frame()
	if !v.avatar.modal.Shown() {
		t.Fatal("no question before deleting")
	}
	if deleted, _ := store.calls(); deleted != 0 {
		t.Fatal("deleted before the answer")
	}
	v.avatar.confirm.click.Click()
	wait("the deletion", func() bool { deleted, _ := store.calls(); return deleted == 1 && !v.open })
	if told != "The photo was deleted." {
		t.Fatalf("told %q", told)
	}

	items = open()
	v.show(items, 1, true)
	frame()
	if v.canDeleteAvatar(items[1]) || !v.canMakeAvatarMain(items[1]) {
		t.Fatal("another of a group's photos: made main, not deleted")
	}
	v.avatar.main.Click()
	wait("the main photo", func() bool { _, main := store.calls(); return main == 1 && !v.open })
	if told != "The photo is the main one now." || store.main[0] != model.ProfilePhotoID(1) {
		t.Fatalf("told %q, made %v main", told, store.main)
	}

	// A message's photo has neither.
	v.gallery = galleryPhotos
	if v.canDeleteAvatar(current) || v.canMakeAvatarMain(items[1]) {
		t.Fatal("offered outside a profile")
	}
}
