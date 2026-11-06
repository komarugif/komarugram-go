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
