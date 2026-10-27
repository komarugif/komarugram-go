// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"time"
)

// PeerProfile is what a chat's info shows over what Chat has, as Telegram
// Desktop's profile: its cover, its usernames and a user's channel.
type PeerProfile struct {
	// Usernames are the chat's active usernames, in Telegram's order.
	Usernames []Username
	// Cover is the background behind the avatar and the name.
	Cover ProfileCover
	// Gifts are the unique gifts pinned to the top of the profile, at most
	// MaxCoverGifts, without the one the emoji status shows: messages with
	// Gift set, as SharedCollection's.
	Gifts []Message
	// Channel is the channel a user put in the profile, nil for none.
	Channel *PersonalChannel
	// About is what a user tells of themselves, or a channel's or a
	// supergroup's description: plain text, whose links and mentions
	// PlainEntities finds.
	About string
	// Phone is a user's phone number, its digits, where they let the
	// account see it; empty otherwise.
	Phone string
	// Badges are the marks beside the name as Telegram says now, when
	// BadgesKnown: a chat's own may be older, as the emoji status of someone
	// with no dialog, which no update reaches.
	Badges      Badges
	BadgesKnown bool
}

// MaxCoverGifts is how many pinned gifts the cover shows around the avatar.
const MaxCoverGifts = 6

// Username is one of a chat's usernames, without "@".
type Username struct {
	Name string
	// Collectible is set for a username bought on Fragment, which the
	// owner cannot edit: CollectibleUsername tells about it.
	Collectible bool
}

// ProfileCover is the background of a profile: the backdrop of the unique
// gift its emoji status is, or else the profile's color; zero for none.
type ProfileCover struct {
	// Gift is set for a unique gift's backdrop: a radial gradient from
	// CenterColor to EdgeColor, the pattern in PatternColor, and text in
	// TextColor; colors are 0xRRGGBB.
	Gift                                            bool
	CenterColor, EdgeColor, PatternColor, TextColor uint32
	// Pattern is the custom emoji drawn around the avatar: the gift's, or
	// the background emoji of the profile's color; 0 for none.
	Pattern int64
	// Colors are the background of the profile's color, for a light theme
	// and a dark one: one color is solid, more a gradient from the second
	// at the center to the first at the edge.
	Colors, DarkColors []uint32
}

// PersonalChannel is the channel of a user's profile and its last post.
type PersonalChannel struct {
	Chat Chat
	// Post is its last post as the chat list shows it, zero without one,
	// but with its text whole, whose Entities, custom emoji among them, it
	// keeps; Date is when it was posted.
	Post     Preview
	Entities []Entity
	Date     time.Time
}

// CollectibleUsername is how a collectible username was bought, as
// fragment.getCollectibleInfo tells.
type CollectibleUsername struct {
	Username string
	Bought   time.Time
	// Amount is the price in Currency's smallest units (cents of "USD"),
	// CryptoAmount in CryptoCurrency's ("TON": a billionth of one).
	Currency       string
	Amount         int64
	CryptoCurrency string
	CryptoAmount   int64
	// URL is the username's page on Fragment.
	URL string
}

// PeerProfileSource tells the profiles of chats.
type PeerProfileSource interface {
	// PeerProfile is the profile of chat; offline, the one known last.
	PeerProfile(ctx context.Context, chat int64) (PeerProfile, error)
	// CachedPeerProfile is the profile of chat known last, without asking
	// Telegram: quick enough to show it at once.
	CachedPeerProfile(chat int64) (PeerProfile, bool)
	// CollectibleUsername tells how the collectible username was bought.
	CollectibleUsername(ctx context.Context, username string) (CollectibleUsername, error)
}
