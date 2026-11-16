package model

import "context"

// MessageDeleter removes explicit message IDs. Channels and supergroups only
// support deletion for everyone; private chats also support local deletion.
type MessageDeleter interface {
	DeleteMessages(context.Context, int64, []MessageID, bool) error
}

// ErasedMessages tells the messages erased from their histories: deleted,
// by anyone, and not kept as deleted ones are. A message gone from a
// history's snapshot is not known to be erased by that alone: it may have
// left what is loaded. A history shows a message that was erased leaving.
type ErasedMessages interface {
	MessageErased(MessageKey) bool
}
