// SPDX-License-Identifier: Unlicense OR MIT

package model

import "context"

// ForwardOptions control the attribution and captions of forwarded messages.
type ForwardOptions struct {
	DropAuthor   bool
	DropCaptions bool // Only valid with DropAuthor.
}

// MessageForwarderWithOptions supports Telegram's forwarding options.
type MessageForwarderWithOptions interface {
	ForwardMessagesWithOptions(ctx context.Context, from int64, ids []MessageID, to int64, options ForwardOptions) error
}

// MessageRights are what may be done with every one of some messages, as
// Telegram Desktop decides for the buttons over a selection.
type MessageRights struct {
	// Forward: none is a service message, and neither the chat nor a
	// message protects its content.
	Forward bool
	// Save: neither the chat nor a message protects its content, so it may
	// be saved, as by a snapshot.
	Save bool
	// Delete: the account may delete each of them, at least for itself.
	Delete bool
	// Revoke: each can be deleted for everyone as well. In channels and
	// supergroups deleting is always for everyone.
	Revoke bool
	// Everyone is set when a chat only deletes for everyone, as channels and
	// supergroups do.
	Everyone bool
}

// RightsSource is a Store that knows what the account may do in its chats.
type RightsSource interface {
	// MessageRights tells what may be done with msgs, messages of chat.
	MessageRights(chat int64, msgs []Message) MessageRights
	// CanSend reports whether the account can send messages to chat, such
	// as forwarded ones.
	CanSend(chat int64) bool
}

// MessageLinker is a Store that knows the t.me links of messages.
type MessageLinker interface {
	// MessageLink is the link of message id of chat, a channel or a
	// supergroup, as Telegram Desktop copies it. public is false for a
	// link that opens only for the chat's members; ok is false when the
	// chat has no links.
	MessageLink(chat int64, id MessageID) (link string, public, ok bool)
}

// MessageForwarder is a Store that forwards messages.
type MessageForwarder interface {
	// ForwardMessages forwards the messages ids of chat from to chat to, in
	// their order.
	ForwardMessages(ctx context.Context, from int64, ids []MessageID, to int64) error
}
