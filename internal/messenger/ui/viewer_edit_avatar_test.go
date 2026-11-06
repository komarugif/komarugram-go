// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// avatarSender records the photos a profile was set to, where it may be.
type avatarSender struct {
	*fakeSender
	allowed bool
	set     []int64
	data    []byte
}

func (a *avatarSender) CanSetProfilePhoto(int64) bool { return a.allowed }
func (a *avatarSender) SetProfilePhoto(_ context.Context, chat int64, path string, _ func(int64, int64)) error {
	a.set = append(a.set, chat)
	a.data, _ = os.ReadFile(path)
	return nil
}

func resultFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.jpg")
	if err := os.WriteFile(path, []byte("the file"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Saved, it goes to the chat given, the account's own, as a message of its own: no
// reply to what was edited.
func TestEditSavedIsAMessageOfItsOwn(t *testing.T) {
	fake := &fakeSender{Store: mockstore.New(time.Now(), 0)}
	m := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 7}, Kind: model.MessagePhoto}
	if _, err := deliverEdited(context.Background(), fake, m, 99, resultFile(t), "", false, editSaved, func(editStage, float64) {}); err != nil {
		t.Fatal(err)
	}
	if len(fake.queued) != 1 || fake.queuedIn[0] != 99 || fake.queued[0].ReplyTo != 0 || string(fake.data) != "the file" {
		t.Fatalf("queued %+v in %v", fake.queued, fake.queuedIn)
	}
}

// Made the main photo, it goes to the profile shown, and is no message.
func TestEditAvatarSetsThePhoto(t *testing.T) {
	fake := &avatarSender{fakeSender: &fakeSender{Store: mockstore.New(time.Now(), 0)}, allowed: true}
	m := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: model.ProfilePhotoID(3)}, Kind: model.MessagePhoto}
	if _, err := deliverEdited(context.Background(), fake, m, 2, resultFile(t), "", false, editAvatar, func(editStage, float64) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fake.set, []int64{2}) || string(fake.data) != "the file" || len(fake.queued) != 0 {
		t.Fatalf("set %v, queued %+v", fake.set, fake.queued)
	}
}

// A profile's photo opens in the editor, and what it makes becomes the main photo
// where it may, or goes to the account's own chat: there is no message to reply
// to, or whose file to replace. Its video is no file to edit.
func TestProfilePhotoEditActions(t *testing.T) {
	fake := &avatarSender{fakeSender: &fakeSender{Store: mockstore.New(time.Now(), 0)}, allowed: true}
	var images imageOps
	v := newMediaViewer(fake, &images, func() {})
	defer v.Destroy()
	v.gallery, v.chat = galleryProfile, 2
	v.savedChat = func() int64 { return 99 }
	photo := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: model.ProfilePhotoID(4)}, Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "photo/9/x"}}
	if !v.canEditNow(photo) {
		t.Fatal("a profile's photo does not open in the editor")
	}
	if got := v.editActions(photo); !slices.Equal(got, []editAction{editAvatar, editSaved}) {
		t.Fatalf("actions %v", got)
	}
	fake.allowed = false
	if got := v.editActions(photo); !slices.Equal(got, []editAction{editSaved}) {
		t.Fatalf("actions where the photo may not be set: %v", got)
	}
	gif := photo
	gif.Kind = model.MessageGIF
	if v.canEditNow(gif) {
		t.Fatal("a profile's video opens in the editor")
	}
	// A message's file is still replied with, and may be saved too.
	v.gallery = galleryPhotos
	m := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 7}, Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "photo/1/x"}}
	got := v.editActions(m)
	if !slices.Contains(got, editReply) || got[len(got)-1] != editSaved || slices.Contains(got, editAvatar) {
		t.Fatalf("actions of a message %v", got)
	}
}

// The account's own chat is where a result saved goes; the others go to the chat
// shown.
func TestEditTargetOfSaved(t *testing.T) {
	var images imageOps
	v := newMediaViewer(mockstore.New(time.Now(), 0), &images, func() {})
	defer v.Destroy()
	v.edit.chat = 2
	v.savedChat = func() int64 { return 99 }
	if v.editTarget(editSaved) != 99 || v.editTarget(editReply) != 2 || v.editTarget(editAvatar) != 2 {
		t.Fatal(v.editTarget(editSaved), v.editTarget(editReply), v.editTarget(editAvatar))
	}
}
