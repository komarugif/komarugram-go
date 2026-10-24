// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"errors"
	"time"
)

// SearchSection is what a search looks for, as the tabs of Telegram
// Desktop's search: chats and their messages, channels, public posts, or
// messages of one kind of media.
type SearchSection int

const (
	SearchChats SearchSection = iota
	SearchChannels
	SearchPosts
	SearchPhotos
	SearchVideos
	SearchLinks
	SearchFiles
	SearchMusic
	SearchVoice
)

// SearchSections are the sections in the order they are offered.
var SearchSections = []SearchSection{SearchChats, SearchChannels, SearchPosts, SearchPhotos, SearchVideos, SearchLinks, SearchFiles, SearchMusic, SearchVoice}

// Local reports whether the local cache can answer the section. Public
// posts come from channels the account may never have opened, so only
// Telegram can find them.
func (s SearchSection) Local() bool { return s != SearchPosts }

// Media reports whether the section is about one kind of media. Without a
// query it lists the latest messages of that kind.
func (s SearchSection) Media() bool { return s >= SearchPhotos }

// SearchQuery is one search: its text, section, and whether Telegram is
// asked (global) or only what this computer has saved (local).
type SearchQuery struct {
	Text    string
	Section SearchSection
	Global  bool
}

// ChatSearchPage is a page of the messages of one chat that a search found,
// newest first. Next asks for the page after it; empty after the last.
// Count is how many were found in all, 0 when it is not known.
type ChatSearchPage struct {
	Messages []Message
	Next     string
	Count    int
}

// ChatSearcher finds messages of one chat, as Telegram Desktop's search in
// a chat does: from Telegram when connected, else in what this computer
// saved.
type ChatSearcher interface {
	SearchChat(ctx context.Context, chat int64, text string, next string, limit int) (ChatSearchPage, error)
}

// FoundMessage is a message found by a search, with the chat it is in.
type FoundMessage struct {
	Message Message
	Chat    Chat
}

// PostsQuota is how many searches of public posts Telegram allows without
// Premium: a query that is not free spends one of the day's free searches.
type PostsQuota struct {
	// Free is set when the query costs nothing, as with Premium or a query
	// already searched.
	Free            bool
	Remains, PerDay int
	// NextFree is when the next free search unlocks, once none remain.
	NextFree time.Time
}

// SearchResults are what the current search found so far.
type SearchResults struct {
	Query SearchQuery
	// Chats are the chats found by name: the account's own first, then
	// public ones when searching globally.
	Chats    []Chat
	Messages []FoundMessage
	// Loading is set while a page is on its way; More when another page
	// can be asked for with SearchMore.
	Loading, More bool
	// Posts is set for public posts that wait for SpendPostsSearch.
	Posts *PostsQuota
	Err   error
}

// ErrSearchOffline is a global search without a connection to Telegram;
// the local search still works.
var ErrSearchOffline = errors.New("no connection to Telegram")

// Searcher is a Store that searches chats and messages. Searches run in the
// background; the store tells of new results as of any other change.
type Searcher interface {
	// Search starts q, abandoning the search before it.
	Search(q SearchQuery)
	// SearchMore asks for the next page of messages.
	SearchMore()
	// SpendPostsSearch searches public posts although the query costs one
	// of the day's free searches.
	SpendPostsSearch()
	SearchResults() SearchResults
}

// MessageRevealer is a Store that can open a chat at a message.
type MessageRevealer interface {
	// Reveal makes the next history of chat load around the message, and
	// its viewport start there.
	Reveal(chat int64, id MessageID)
}

// UnreadOpener is a Store that opens a chat at its first message not read
// when the chat comes back with such messages: left at its end, it would
// open there and read them all at once.
type UnreadOpener interface {
	// OpenUnread is told each time the history shows chat anew.
	OpenUnread(chat int64)
}

// HistoryEnds is a Store that can open a chat at either end of its history,
// as the buttons that scroll to the start and to the end do when the loaded
// part of the history is somewhere in the middle.
type HistoryEnds interface {
	// RevealFirst makes the next history of chat load from its oldest
	// message. It reports false for a chat this does not work for.
	RevealFirst(chat int64) bool
	// RevealLast is RevealFirst for the newest message.
	RevealLast(chat int64) bool
}
