// SPDX-License-Identifier: Unlicense OR MIT

package model

// SendState is how far a message the account sent has gone, as the marks
// beside its time show.
type SendState uint8

const (
	// SendNone: no mark, as for a message the account did not send.
	SendNone SendState = iota
	// SendSending: on its way to Telegram, a clock.
	SendSending
	// SendSent: Telegram has it, one check.
	SendSent
	// SendRead: the other side read it, two checks.
	SendRead
)

// MessageSendState is the state of m, a message of a chat of kind whose
// other side read the account's messages up to readOutbox. As in Telegram
// Desktop, a channel's posts carry no mark, and the account's messages in
// Saved Messages and to bots are read as they come.
func MessageSendState(m Message, kind ChatKind, readOutbox MessageID) SendState {
	switch {
	case m.Sending:
		return SendSending
	case !m.Outgoing || m.Post || m.Kind == MessageService || m.Streaming:
		return SendNone
	}
	return sentState(m.Key.MessageID, kind, readOutbox)
}

// LastSendState is the state of the last message of c, as its list shows
// it.
func (c Chat) LastSendState() SendState {
	if !c.Last.Sent || c.Last.Service != nil || c.Kind == KindChannel {
		return SendNone
	}
	return sentState(c.Last.ID, c.Kind, c.ReadOutbox)
}

func sentState(id MessageID, kind ChatKind, readOutbox MessageID) SendState {
	if kind == KindSaved || kind == KindBot || id <= readOutbox {
		return SendRead
	}
	return SendSent
}
