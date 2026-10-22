// SPDX-License-Identifier: Unlicense OR MIT

package model

import "context"

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
