// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"errors"
	"sort"
)

// ErrPinnedTooMuch is what pinning a chat comes to when the account has
// pinned as many as its limit lets it (the "dialogs_pinned_limit" of
// PremiumLimits).
var ErrPinnedTooMuch = errors.New("chats: too many pinned chats")

// ChatListActions is a Store that can do what the menu of a chat in the
// list offers.
type ChatListActions interface {
	// PinChat pins chat to the top of the list, or unpins it. The list
	// changes at once on success; the error says why it did not.
	PinChat(ctx context.Context, chat int64, pin bool) error
	// MarkChatRead marks all of chat's messages read.
	MarkChatRead(chat int64)
}

// SortChats puts chats in the order of the list: the pinned ones first, in
// their pin order, then by the last message, the newest first.
func SortChats(chats []Chat) {
	sort.SliceStable(chats, func(i, j int) bool {
		a, b := chats[i], chats[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if a.Pinned {
			return a.PinRank < b.PinRank
		}
		return a.SortTime().After(b.SortTime())
	})
}

// PinnedCount is how many of chats are pinned.
func PinnedCount(chats []Chat) int {
	n := 0
	for _, c := range chats {
		if c.Pinned {
			n++
		}
	}
	return n
}

// WithPinned returns chats with chat pinned, and first among the pinned as
// Telegram puts it, or unpinned; the order of the rest is kept. chats is
// not changed.
func WithPinned(chats []Chat, chat int64, pin bool) []Chat {
	out := append([]Chat(nil), chats...)
	for i := range out {
		if out[i].ID == chat {
			out[i].Pinned = pin
			out[i].PinRank = 0
			if pin {
				out[i].PinRank = -1
			}
		}
	}
	return Renumbered(out)
}

// Renumbered returns chats, sorted, with the ranks of the pinned ones from 1
// without gaps.
func Renumbered(chats []Chat) []Chat {
	SortChats(chats)
	rank := 0
	for i := range chats {
		if chats[i].Pinned {
			rank++
			chats[i].PinRank = rank
		} else {
			chats[i].PinRank = 0
		}
	}
	return chats
}

// WithPinOrder returns chats with exactly the chats of order pinned, in that
// order, as an update of the pinned chats says. Chats of order that are not
// in the list are left out.
func WithPinOrder(chats []Chat, order []int64) []Chat {
	out := append([]Chat(nil), chats...)
	rank := make(map[int64]int, len(order))
	for i, id := range order {
		rank[id] = i + 1
	}
	for i := range out {
		r := rank[out[i].ID]
		out[i].Pinned, out[i].PinRank = r > 0, r
	}
	return Renumbered(out)
}
