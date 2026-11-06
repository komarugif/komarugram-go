// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"errors"
)

// profilePhotoBase is below every message and story of a chat, so that the
// photos of its profile are told from them.
const profilePhotoBase MessageID = -1 << 30

// ProfilePhotoID is the message ID that stands for photo i of a chat's
// profile, the one it shows now being 0 and older ones following.
func ProfilePhotoID(i int) MessageID { return profilePhotoBase + MessageID(i) }

// IsProfilePhoto tells whether id stands for a photo of a chat's profile.
func IsProfilePhoto(id MessageID) bool { return id >= profilePhotoBase && id < profilePhotoBase/2 }

// ProfilePhotoSource gives the photos of a chat's profile (a user's, a
// group's, a channel's) as photo messages of that chat with the IDs of
// ProfilePhotoID, for the media viewer.
type ProfilePhotoSource interface {
	// ProfilePhoto is the photo the chat shows now, as far as it is known
	// without asking Telegram: the size the chat list has among its
	// variants, so that something shows at once.
	ProfilePhoto(chat int64) (Message, bool)
	// ProfilePhotos asks for a page of the photos of the chat's profile,
	// the one it shows now first: from offset, the Next of the page before
	// it, or from the first photo when offset is empty.
	ProfilePhotos(ctx context.Context, chat int64, offset string, limit int) (GalleryPage, error)
}

// ErrProfilePhotoForbidden is what setting a profile's photo fails with where the
// account may not change it.
var ErrProfilePhotoForbidden = errors.New("the profile's photo may not be changed")

// ProfilePhotoSetter makes a picture the main photo of a profile: the account's
// own, or a group's or channel's whose information it may change.
type ProfilePhotoSetter interface {
	// CanSetProfilePhoto tells whether SetProfilePhoto may set chat's photo.
	CanSetProfilePhoto(chat int64) bool
	// SetProfilePhoto uploads the picture at path, the largest square in its
	// middle, and makes it chat's main photo, telling how much of it went.
	SetProfilePhoto(ctx context.Context, chat int64, path string, progress func(done, total int64)) error
}

// ProfilePhotoEditor deletes the photos of a profile and makes one of them its main
// one, as Telegram Desktop's viewer offers: any of the account's own, and a group's
// or channel's current one where the account may change its information.
type ProfilePhotoEditor interface {
	// CanDeleteProfilePhoto tells whether photo m of a profile may be deleted.
	CanDeleteProfilePhoto(m Message) bool
	// DeleteProfilePhoto deletes photo m of its profile: the current one is
	// taken off it.
	DeleteProfilePhoto(ctx context.Context, m Message) error
	// CanMakeProfilePhotoMain tells whether photo m, not the profile's main one,
	// may become it.
	CanMakeProfilePhotoMain(m Message) bool
	// MakeProfilePhotoMain makes photo m its profile's main photo.
	MakeProfilePhotoMain(ctx context.Context, m Message) error
}
