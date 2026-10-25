// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"time"
)

// A story is a Message of the peer who posted it, its MessageID the story's
// id below zero (StoryMessage), so that it is in no history: a photo or a
// video, its caption the text, Views who saw it, NoForwards set when it may
// not be shared, ForwardName whose story it reposts, and Story what only a
// story has.

// Story is what a story has beyond a message's.
type Story struct {
	// Likes counts the reactions to it, and Liked is set when the account
	// reacted, as Telegram Desktop's heart tells.
	Likes int  `json:",omitempty"`
	Liked bool `json:",omitempty"`
	// Expires is when it is no longer shown; a story kept on its peer's
	// profile still is there.
	Expires time.Time `json:",omitempty"`
}

// StoryMessage is the MessageID of story id.
func StoryMessage(id int) MessageID { return MessageID(-id) }

// StoryID is the id of the story of a message StoryMessage made.
func StoryID(id MessageID) int { return int(-id) }

// StoryPeer is someone with stories now, as the list of chats shows them
// over the chats.
type StoryPeer struct {
	ID    int64
	Kind  ChatKind
	Title string
	// Self marks the account's own stories.
	Self bool
	// Count is how many stories it has now, Unread how many of them the
	// account has not seen, which are the last ones.
	Count, Unread int
	// Last is when the last one was posted.
	Last time.Time
	// Open is the story to open first: the first unread, or the first.
	Open MessageID
}

// StorySource is a Store that knows the stories of the account's contacts
// and channels, as Telegram Desktop's list shows them over the chats.
type StorySource interface {
	// StoryPeers are those with stories now, in the order of the list:
	// the account first, then those with unread ones, the latest first.
	// The slice is the store's own; it is not changed but replaced.
	StoryPeers() []StoryPeer
	// PeerStories are peer's stories now, the oldest first, asked of
	// Telegram for those it has not told in full yet.
	PeerStories(ctx context.Context, peer int64) ([]Message, error)
	// ReadStory marks the stories of peer up to id seen, and tells
	// Telegram so when Ghost allows.
	ReadStory(peer int64, id MessageID)
	// LikeStory puts the account's heart on story id of peer, or takes it
	// away.
	LikeStory(ctx context.Context, peer int64, id MessageID, like bool) error
	// StoryNow is story id of peer as the store knows it now: its likes
	// change after it was shown.
	StoryNow(peer int64, id MessageID) (Message, bool)
	// ShareStory sends story id of peer to chat to.
	ShareStory(ctx context.Context, peer int64, id MessageID, to int64) error
}
