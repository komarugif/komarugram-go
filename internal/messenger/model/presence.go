// SPDX-License-Identifier: Unlicense OR MIT

package model

import "time"

// Seen is how much a user lets others know of when they were online, as
// Telegram's userStatus tells it.
type Seen uint8

const (
	// SeenUnknown: nothing is known yet.
	SeenUnknown Seen = iota
	// SeenAt: the user is online until At, or was last online at At.
	SeenAt
	// SeenRecently, SeenLastWeek and SeenLastMonth: the user hides the time,
	// and was online that long ago.
	SeenRecently
	SeenLastWeek
	SeenLastMonth
	// SeenLongAgo: the user was not online for a long time, or blocked the
	// account.
	SeenLongAgo
)

// Presence is when a user was online.
type Presence struct {
	Seen Seen
	// At is till when the user is online, or when they were last, for
	// SeenAt.
	At time.Time
}

// Online reports whether the user is online at now.
func (p Presence) Online(now time.Time) bool {
	return p.Seen == SeenAt && now.Before(p.At)
}

// PresenceSource knows when users were online.
type PresenceSource interface {
	// Presence is when the user of the private chat chat was online, as
	// far as the store knows; false when it knows nothing.
	Presence(chat int64) (Presence, bool)
	// RefreshPresence asks Telegram again, at most once a minute, when the
	// user of chat was online; the store tells of the answer as of any
	// change. It never blocks.
	RefreshPresence(chat int64)
}
