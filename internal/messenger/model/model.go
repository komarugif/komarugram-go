// SPDX-License-Identifier: Unlicense OR MIT

// Package model holds the messenger data types the UI works with, and the
// Store interface that provides them. The UI only sees a Store, so the stub
// data of mockstore can later be replaced by a real account.
package model

import (
	"image"
	"time"
)

// ChatKind is the kind of a chat.
type ChatKind int

const (
	KindUser ChatKind = iota
	KindGroup
	KindChannel
	KindBot
	// KindSaved is the user's own chat, "Saved Messages".
	KindSaved
)

// Preview is the last message of a chat or a topic as its list shows it.
// The interface words what is not the message's own words: Media and
// Service, and the account as the sender.
type Preview struct {
	// Text is the message's text on one line, or a line the interface
	// worded already.
	Text string `json:",omitempty"`
	// Sender is who wrote it, in a group; Outgoing is set when the account
	// did there.
	Sender   string `json:",omitempty"`
	Outgoing bool   `json:",omitempty"`
	// Media is the key of the interface's text naming what the message
	// carries ("preview.photo"), shown before Text; Emoji is a sticker's,
	// or what a dice message carries when Media is empty.
	Media string `json:",omitempty"`
	Emoji string `json:",omitempty"`
	// Service is the message when it is a service one.
	Service *Message `json:",omitempty"`
	// ID is the message's; Sent is set when the account sent it, in any
	// chat, for the list to mark whether it was read.
	ID   MessageID `json:",omitempty"`
	Sent bool      `json:",omitempty"`
}

type Chat struct {
	ID   int64
	Kind ChatKind
	// Title is the chat's name; empty for Saved Messages and a deleted
	// account, which the interface names.
	Title string
	// Last is the chat's last message, as the list shows it.
	Last     Preview
	LastTime time.Time
	Unread   int
	// ReadOutbox is the newest of the account's messages the other side
	// read; those before it are read too.
	ReadOutbox MessageID `json:",omitempty"`
	// ReadInbox is the newest of the others' messages the account read, on
	// any of its devices: a chat not opened here opens after it.
	ReadInbox MessageID `json:",omitempty"`
	Muted     bool
	// Pinned chats stay at the top of the list, in the order PinRank gives:
	// 1 for the first, 0 for a chat that is not pinned.
	Pinned  bool
	PinRank int
	// Members is the member or subscriber count of groups and channels.
	Members int
	// Forum is set for a group divided in topics, which open as a list of
	// them: see ForumSource.
	Forum bool
	// DirectMessages is set for the chat of a channel's direct messages
	// (DirectMessagesSource), a group of Telegram's that the account reads
	// as a private chat with the channel: without avatars.
	DirectMessages bool `json:",omitempty"`
	// Badges are the marks beside the chat's name.
	Badges
}

// Badges are the marks Telegram shows around a name.
type Badges struct {
	// Premium is set for a user with Telegram Premium, and EmojiStatus is
	// the custom emoji a user or channel shows beside the name instead of the
	// Premium star, 0 for none.
	Premium     bool
	EmojiStatus int64
	// Verified is Telegram's own check mark.
	Verified bool
	// Scam and Fake are Telegram's warnings; either replaces every other
	// mark after the name.
	Scam, Fake bool
	// BotVerification is the custom emoji a verifying bot (a third party,
	// such as a Mini App) put before the name, 0 for none.
	BotVerification int64
	// User is the user the badges belong to, 0 for a chat that is not one.
	// It lets the client tell an account of its own, which Local Premium
	// marks, from any other.
	User int64
	// Channel is the channel the badges belong to, 0 for a chat that is not
	// one: the bare id its official mark is looked up by.
	Channel int64
}

// Folder is a chat folder. It holds the chats Match accepts or, without
// Match, the chats of the given kinds.
type Folder struct {
	ID    int64
	Title string
	// Kinds are the kinds of chat the folder is about. They also pick its
	// icon, so a folder with Match may name them too.
	Kinds []ChatKind
	// Match decides membership when a folder is more than a set of kinds, as
	// a Telegram folder is: its rules name chats as well as kinds.
	Match func(Chat) bool
}

// Contains reports whether the chat belongs to the folder.
func (f Folder) Contains(c Chat) bool {
	if f.Match != nil {
		return f.Match(c)
	}
	for _, k := range f.Kinds {
		if c.Kind == k {
			return true
		}
	}
	return false
}

type Profile struct {
	// ID is the account's user id, 0 when unknown.
	ID int64
	// DC is the account's home data center, 0 when unknown.
	DC        int
	FirstName string
	LastName  string
	Username  string
	Phone     string
	Bio       string
	// Badges are the marks beside the account's name.
	Badges
}

// Name returns the full name.
func (p Profile) Name() string {
	if p.LastName == "" {
		return p.FirstName
	}
	return p.FirstName + " " + p.LastName
}

// Store provides the account data.
type Store interface {
	// Me returns the profile of the account.
	Me() Profile
	// Folders returns the chat folders in display order.
	Folders() []Folder
	// Chats returns all chats, pinned first, then newest first.
	Chats() []Chat
}

// AccountInfo is the process-wide summary shown in Settings. It is known for
// every saved account, connected or not.
type AccountInfo struct {
	ID string
	// UserID is the Telegram user id, which also picks the placeholder
	// avatar's color.
	UserID   int64
	Name     string
	Username string
	Phone    string
	Premium  bool
	// Avatar is the small profile photo, nil when there is none or it is not
	// known yet.
	Avatar image.Image
	Open   bool
}

// Accounts exposes all saved accounts and opens or focuses their windows.
// Implementations notify subscribers when profiles or open-window state
// changes.
type Accounts interface {
	All() []AccountInfo
	Open(id string)
	// Add opens a window that signs in to another account, or shows the one
	// already doing it.
	Add()
	// LogOut ends the account's session in Telegram, closes its window and
	// deletes its data from this computer.
	LogOut(id string)
	Subscribe(func()) func()
}
