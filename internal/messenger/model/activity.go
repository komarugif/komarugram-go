// SPDX-License-Identifier: Unlicense OR MIT

package model

import "time"

// ChatAction is what someone does in a chat that Telegram tells the others
// of while it goes on (messages.setTyping): types, records, sends a file.
type ChatAction uint8

const (
	ActionNone ChatAction = iota
	ActionTyping
	ActionRecordVoice
	ActionUploadVoice
	ActionRecordVideo
	ActionUploadVideo
	ActionRecordRound
	ActionUploadRound
	ActionUploadPhoto
	ActionUploadFile
	ActionChooseLocation
	ActionChooseContact
	ActionChooseSticker
	ActionPlayGame
)

// Activity is someone doing something in a chat now.
type Activity struct {
	// User is who, Name their first name.
	User int64
	Name string
	// Top is the thread it is in, the root of a forum's topic or of a
	// post's comments; 0 for the chat itself.
	Top    int
	Action ChatAction
	// Progress is how much of an upload is done, in percent.
	Progress int
	// Until is when it is over unless told again: Telegram Desktop shows
	// an action six seconds after it was told, a game ten.
	Until time.Time
}

// ActivitySource tells what others do in chats now.
type ActivitySource interface {
	// Activities are those of chat, oldest first, with those already over
	// that the store has not let go yet: a caller skips what ended before
	// now. The slice is the store's, not to be changed; a new one replaces
	// it when anything changes.
	Activities(chat int64) []Activity
}
