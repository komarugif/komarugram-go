// SPDX-License-Identifier: Unlicense OR MIT
package model

import "context"

// SharedKind selects Telegram's server-side media index, independent of history.
type SharedKind string

const (
	SharedPhotos SharedKind = "photos"
	SharedVideos SharedKind = "videos"
	SharedFiles  SharedKind = "files"
	SharedMusic  SharedKind = "music"
	SharedLinks  SharedKind = "links"
	SharedVoice  SharedKind = "voice"
	SharedGIFs   SharedKind = "gifs"
	SharedPolls  SharedKind = "polls"
	SharedSaved  SharedKind = "saved"
	// SharedPhotoVideos is the media viewer's gallery of photos and videos, not
	// a section.
	SharedPhotoVideos SharedKind = "photo_videos"
)

var SharedKinds = []SharedKind{SharedSaved, SharedPhotos, SharedVideos, SharedFiles, SharedMusic, SharedLinks, SharedPolls, SharedVoice, SharedGIFs}

type SharedPage struct {
	Messages []Message
	Total    int
	Next     MessageID
	More     bool
}
type SharedMediaSource interface {
	SharedCounts(context.Context, int64) (map[SharedKind]int, error)
	SharedMedia(context.Context, int64, SharedKind, MessageID, int) (SharedPage, error)
}

const (
	SharedStories SharedKind = "stories"
	// SharedStoryArchive are the account's own stories, those its profile
	// does not show too: only its own profile has them.
	SharedStoryArchive SharedKind = "story_archive"
	// SharedSavedMusic is the playlist of a user's profile: the songs they
	// put on it, their profile's song (PeerProfile.Music) first.
	SharedSavedMusic SharedKind = "saved_music"
	SharedGifts      SharedKind = "gifts"
	SharedGroups     SharedKind = "groups"
)

// Collections use opaque offsets because gift pagination is not a message ID.
type SharedCollectionSource interface {
	SharedCollection(context.Context, int64, SharedKind, string, int) (SharedCollectionPage, error)
}
type SharedCollectionPage struct {
	Messages []Message
	Total    int
	Next     string
}
