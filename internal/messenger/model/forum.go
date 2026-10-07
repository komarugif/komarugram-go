// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"time"
)

// Topic is one of the topics of a forum: a group whose messages are sorted
// into threads that members open and name.
type Topic struct {
	// ID is the topic's first message, which the topic is named by; the
	// General topic is GeneralTopic.
	ID    int
	Title string
	// IconColor is the color of the topic's icon, 0xRRGGBB; IconEmoji the
	// custom emoji it shows instead of the default mark, 0 for none.
	IconColor int
	IconEmoji int64
	// General marks the topic every forum has. Closed topics take no new
	// messages from members; Pinned ones come first.
	General, Closed, Pinned, Muted bool
	// Unread counts the messages not read, Mentions those that name the
	// account.
	Unread, Mentions int
	LastSender       string
	LastMessage      string
	LastTime         time.Time
}

// TopicList is what a forum's topics look like so far.
type TopicList struct {
	Topics []Topic
	// Loading is set while a page is on its way; More when another page can
	// be asked for with LoadMoreTopics.
	Loading, More bool
	Err           error
}

// ForumSource is a Store that lists the topics of forums and opens them.
type ForumSource interface {
	// OpenForum starts reading the topics of forum, or reads them again when
	// they are old; the store tells of them as of any other change.
	OpenForum(chat int64)
	// Topics returns the topics read so far, in their order: pinned ones
	// first, then by their last message.
	Topics(chat int64) TopicList
	// LoadMoreTopics asks for the next page of topics.
	LoadMoreTopics(chat int64)
	// OpenTopic returns the chat that shows the messages of topic, as
	// CommentsStore.OpenComments does for the comments to a post: it loads
	// as any chat's history does, and what is sent to it goes into the topic.
	OpenTopic(chat int64, topic Topic) Chat
}

// ForumSearcher searches all the topics of a forum, as Telegram Desktop
// searches a forum from its list of topics, and opens a topic at a message
// found.
type ForumSearcher interface {
	// SearchForum returns a page of the messages of forum that match text,
	// newest first, each with its topic; next continues it.
	SearchForum(ctx context.Context, forum int64, text, next string, limit int) (ForumSearchPage, error)
	// OpenTopicAt is OpenTopic, loading the topic around message at and
	// opening it there.
	OpenTopicAt(chat int64, topic Topic, at MessageID) Chat
}

// ForumSearchPage is a page of what a forum's search found.
type ForumSearchPage struct {
	Found []FoundInTopic
	// Count is how many messages match in all; Next asks for the next
	// page, "" when there is none.
	Count int
	Next  string
}

// FoundInTopic is a message a forum's search found, with its topic.
type FoundInTopic struct {
	Message Message
	Topic   Topic
}
