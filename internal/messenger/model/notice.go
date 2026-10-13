// SPDX-License-Identifier: Unlicense OR MIT

package model

// MessageNotice is a message that just came to the account, for a desktop
// notification.
type MessageNotice struct {
	Chat Chat
	// Message is the message as the chat list shows it.
	Message Preview
	// Silent is set when the sender sent it without sound.
	Silent bool
}
