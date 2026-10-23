// SPDX-License-Identifier: Unlicense OR MIT

package model

import "time"

// RecentChats is the search history, as Telegram Desktop keeps it
// (Data::RecentPeers): the chats picked from search results, the last one
// first, which the search shows while nothing is typed. It stays on this
// computer.
type RecentChats interface {
	RecentChats() []Chat
	// BumpRecentChat puts c first, as when it is picked from the results.
	BumpRecentChat(c Chat)
	RemoveRecentChat(id int64)
	ClearRecentChats()
}

// PeerBadgesSource is a store that knows the marks beside the name of a peer
// as of the last it heard of them, which a chat kept apart from the chat list,
// such as one picked from the search, is not told of.
type PeerBadgesSource interface {
	// PeerBadges are the badges of the user or channel id, false when it is
	// not known or has none to tell.
	PeerBadges(id int64) (Badges, bool)
}

// RecentChatsLimit is how many chats the history keeps, as Telegram
// Desktop's does.
const RecentChatsLimit = 48

// BumpChat returns list with c first, once, and no more than limit chats.
// A chat is kept by what identifies and names it, not by what it last said.
func BumpChat(list []Chat, c Chat, limit int) []Chat {
	c.Last, c.Unread, c.LastTime, c.Pinned, c.PinRank = Preview{}, 0, time.Time{}, false, 0
	out := make([]Chat, 0, min(len(list)+1, limit))
	out = append(out, c)
	for _, old := range list {
		if old.ID != c.ID && len(out) < limit {
			out = append(out, old)
		}
	}
	return out
}
