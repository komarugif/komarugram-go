// SPDX-License-Identifier: Unlicense OR MIT

package model

// Ghost is what the account tells others of itself, as AyuGram's Ghost
// Mode: the zero value tells nothing, which is this client's way.
type Ghost struct {
	// SendRead marks messages read as they are shown; SendOnline shows the
	// account online while a window is active.
	SendRead, SendOnline bool
	// SendTyping tells a chat the account types in it, or chooses a place
	// or a contact; SendUploads, that it sends a photo, a video, a file or
	// a voice message; SendRecording, that it records a voice message;
	// SendStickers, that it chooses a sticker.
	SendTyping, SendUploads, SendRecording, SendStickers bool
	// ReadOnInteract marks a chat read, without SendRead, when the account
	// sends to it or reacts in it.
	ReadOnInteract bool
}

// Sends reports whether g tells others of action.
func (g Ghost) Sends(action ChatAction) bool {
	switch action {
	case ActionTyping, ActionChooseLocation, ActionChooseContact:
		return g.SendTyping
	case ActionUploadPhoto, ActionUploadVideo, ActionUploadVoice, ActionUploadRound, ActionUploadFile:
		return g.SendUploads
	case ActionRecordVoice, ActionRecordVideo, ActionRecordRound:
		return g.SendRecording
	case ActionChooseSticker:
		return g.SendStickers
	}
	return false
}

// GhostStore sends what Ghost allows.
type GhostStore interface {
	SetGhost(Ghost)
	Ghost() Ghost
	// MarkRead marks chat read up to message id, when Ghost.SendRead, or
	// always when asked, as by the menu's Read Message.
	MarkRead(chat int64, id MessageID, asked bool)
	// SetOnline tells whether a window of the account is active.
	SetOnline(bool)
	// SendAction tells chat what the account does in it, when Ghost
	// allows: a store tells it again only every few seconds, however often
	// it is called, and not where nobody would see it.
	SendAction(chat int64, action ChatAction)
}
