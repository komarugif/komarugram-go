// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

var _ model.ProfilePhotoSetter = (*Store)(nil)

// CanSetProfilePhoto implements model.ProfilePhotoSetter: the account's own photo,
// and a group's or channel's whose information it may change.
func (s *Store) CanSetProfilePhoto(chat int64) bool {
	if s.Freeze() != (model.Freeze{}) {
		return false
	}
	c := s.history
	c.mu.Lock()
	peer, ok := c.peers[chat]
	c.mu.Unlock()
	if !ok {
		return false
	}
	switch peer.Kind {
	case "user":
		return peer.Rights.Self
	case "chat", "channel":
		return peer.Rights.ChangeInfo
	}
	return false
}

// SetProfilePhoto implements model.ProfilePhotoSetter, as Telegram Desktop's
// PeerPhoto::upload: the account's own by photos.uploadProfilePhoto, a group's by
// messages.editChatPhoto, a channel's or supergroup's by channels.editPhoto.
func (s *Store) SetProfilePhoto(ctx context.Context, chat int64, path string, progress func(done, total int64)) error {
	if !s.CanSetProfilePhoto(chat) {
		return model.ErrProfilePhotoForbidden
	}
	photo, err := sendfiles.PrepareProfilePhoto(path)
	if err != nil {
		return err
	}
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return err
	}
	defer done()
	up := uploader.NewUploader(api).WithProgress(photoUploadProgress(progress))
	file, err := up.FromBytes(ctx, "photo.jpg", photo.JPEG)
	if err != nil {
		return err
	}
	return profilePhotoError(s.setPhoto(ctx, api, peer, file))
}

// profilePhotoError is what setting a profile's photo failed with, as the viewer
// tells it.
func profilePhotoError(err error) error {
	switch {
	case tgerr.Is(err, "CHAT_ADMIN_REQUIRED", "CHAT_FORBIDDEN", "CHANNEL_PRIVATE", "RIGHT_FORBIDDEN"):
		return fmt.Errorf("%w: %w", model.ErrProfilePhotoForbidden, err)
	case tgerr.Is(err, "PHOTO_CROP_SIZE_SMALL", "PHOTO_INVALID_DIMENSIONS", "PHOTO_INVALID", "PHOTO_EXT_INVALID", "PHOTO_FILE_MISSING"):
		return fmt.Errorf("%w: %w", model.ErrMediaInvalid, err)
	}
	return err
}

// setPhoto makes file, uploaded, peer's photo.
func (s *Store) setPhoto(ctx context.Context, api *tg.Client, peer peerRecord, file tg.InputFileClass) error {
	switch peer.Kind {
	case "user":
		res, err := api.PhotosUploadProfilePhoto(ctx, &tg.PhotosUploadProfilePhotoRequest{File: file})
		if err != nil {
			return err
		}
		// The account with its new photo: no update tells it.
		s.rememberPeers(res.Users, nil)
		s.changed()
		return nil
	case "chat":
		// The updates of the reply go to the manager, as every request's do.
		_, err := api.MessagesEditChatPhoto(ctx, &tg.MessagesEditChatPhotoRequest{ChatID: peer.ID, Photo: &tg.InputChatUploadedPhoto{File: file}})
		return err
	case "channel":
		_, err := api.ChannelsEditPhoto(ctx, &tg.ChannelsEditPhotoRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, Photo: &tg.InputChatUploadedPhoto{File: file}})
		return err
	}
	return errors.New("no photo to set")
}

// photoUploadProgress tells progress how much of a file went.
type photoUploadProgress func(done, total int64)

func (p photoUploadProgress) Chunk(_ context.Context, state uploader.ProgressState) error {
	if p != nil {
		p(state.Uploaded, state.Total)
	}
	return nil
}

var _ model.ProfilePhotoEditor = (*Store)(nil)

// profilePhotoOf is where photo m of a profile is, the profile it is of, and
// whether it is that profile's current photo; ok is false for no such photo.
func (s *Store) profilePhotoOf(m model.Message) (ref fileLocation, peer peerRecord, current, ok bool) {
	if m.Media == nil || !model.IsProfilePhoto(m.Key.MessageID) || s.Freeze() != (model.Freeze{}) {
		return ref, peer, false, false
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, known := c.refs[m.Media.ID]
	peer, ok = c.peers[m.Key.ChatID]
	if !known || !ok || ref.ID == 0 {
		return ref, peer, false, false
	}
	return ref, peer, peer.Photo != nil && peer.Photo.ID == ref.ID, true
}

// mayChangePhoto tells whether the account may change peer's photo.
func mayChangePhoto(peer peerRecord) bool {
	switch peer.Kind {
	case "user":
		return peer.Rights.Self
	case "chat", "channel":
		return peer.Rights.ChangeInfo
	}
	return false
}

// CanDeleteProfilePhoto implements model.ProfilePhotoEditor, as Telegram Desktop's
// viewer allows: any photo of the account's own, a group's or channel's current one.
func (s *Store) CanDeleteProfilePhoto(m model.Message) bool {
	_, peer, current, ok := s.profilePhotoOf(m)
	return ok && mayChangePhoto(peer) && (peer.Kind == "user" || current)
}

// CanMakeProfilePhotoMain implements model.ProfilePhotoEditor: a photo of the list
// that is not the main one, of a profile whose photo the account may change.
func (s *Store) CanMakeProfilePhotoMain(m model.Message) bool {
	ref, peer, current, ok := s.profilePhotoOf(m)
	return ok && !current && ref.Photo && mayChangePhoto(peer)
}

// DeleteProfilePhoto implements model.ProfilePhotoEditor, as Telegram Desktop's
// PeerPhoto::clear: the current photo is taken off the profile, the account's
// other ones are deleted.
func (s *Store) DeleteProfilePhoto(ctx context.Context, m model.Message) error {
	if !s.CanDeleteProfilePhoto(m) {
		return model.ErrProfilePhotoForbidden
	}
	ref, _, current, _ := s.profilePhotoOf(m)
	ctx, api, peer, _, done, err := s.peerOperation(ctx, m.Key.ChatID)
	if err != nil {
		return err
	}
	defer done()
	if !current {
		err = s.withPhotoReference(ctx, api, peer, m, ref, func(photo *tg.InputPhoto) error {
			_, err := api.PhotosDeletePhotos(ctx, []tg.InputPhotoClass{photo})
			return err
		})
		return profilePhotoError(err)
	}
	return profilePhotoError(s.changePhoto(ctx, api, peer, &tg.InputPhotoEmpty{}))
}

// MakeProfilePhotoMain implements model.ProfilePhotoEditor, as Telegram Desktop's
// PeerPhoto::set.
func (s *Store) MakeProfilePhotoMain(ctx context.Context, m model.Message) error {
	if !s.CanMakeProfilePhotoMain(m) {
		return model.ErrProfilePhotoForbidden
	}
	ref, _, _, _ := s.profilePhotoOf(m)
	ctx, api, peer, _, done, err := s.peerOperation(ctx, m.Key.ChatID)
	if err != nil {
		return err
	}
	defer done()
	return profilePhotoError(s.withPhotoReference(ctx, api, peer, m, ref, func(photo *tg.InputPhoto) error {
		return s.changePhoto(ctx, api, peer, photo)
	}))
}

// withPhotoReference calls f with photo ref, and once more with its reference
// renewed when Telegram says it expired.
func (s *Store) withPhotoReference(ctx context.Context, api *tg.Client, peer peerRecord, m model.Message, ref fileLocation, f func(*tg.InputPhoto) error) error {
	err := f(&tg.InputPhoto{ID: ref.ID, AccessHash: ref.Hash, FileReference: ref.Reference})
	if !tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_EMPTY", "FILE_REFERENCE_INVALID") {
		return err
	}
	if err := s.refreshProfilePhoto(ctx, api, peer, ref); err != nil {
		return err
	}
	c := s.history
	c.mu.Lock()
	ref = c.refs[m.Media.ID]
	c.mu.Unlock()
	return f(&tg.InputPhoto{ID: ref.ID, AccessHash: ref.Hash, FileReference: ref.Reference})
}

// changePhoto makes photo peer's photo, or none: the account's own by
// photos.updateProfilePhoto, a group's by messages.editChatPhoto, a channel's by
// channels.editPhoto.
func (s *Store) changePhoto(ctx context.Context, api *tg.Client, peer peerRecord, photo tg.InputPhotoClass) error {
	var chatPhoto tg.InputChatPhotoClass = &tg.InputChatPhotoEmpty{}
	if p, ok := photo.(*tg.InputPhoto); ok {
		chatPhoto = &tg.InputChatPhoto{ID: p}
	}
	switch peer.Kind {
	case "user":
		res, err := api.PhotosUpdateProfilePhoto(ctx, &tg.PhotosUpdateProfilePhotoRequest{ID: photo})
		if err != nil {
			return err
		}
		// The account with its photo: no update tells it.
		s.rememberPeers(res.Users, nil)
		s.changed()
		return nil
	case "chat":
		_, err := api.MessagesEditChatPhoto(ctx, &tg.MessagesEditChatPhotoRequest{ChatID: peer.ID, Photo: chatPhoto})
		return err
	case "channel":
		_, err := api.ChannelsEditPhoto(ctx, &tg.ChannelsEditPhotoRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, Photo: chatPhoto})
		return err
	}
	return errors.New("no photo to change")
}
