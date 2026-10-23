// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A text message the account sends shows at the end of its history while
// Telegram has not taken it, with the clock of Telegram Desktop's sending
// messages, and goes once the message Telegram made of it is there. The
// cache keeps none of it. Files are left out: their upload shows nothing
// yet to stand in for them.

// pendingIDBase keeps the ids of messages on their way below those of the
// drafts bots stream, which count down from -1.
const pendingIDBase = 1 << 30

// pendingMessage is a message the account sent that Telegram has not taken
// yet.
type pendingMessage struct {
	// chat is the chat it shows in, a thread's own id for a comment; seq
	// orders the messages and makes their ids.
	chat int64
	seq  int
	msg  model.Message
	// id is the id Telegram gave it, 0 before: once the history has it,
	// the message stands in for nothing.
	id model.MessageID
}

// pendingMessages are the account's messages on their way, oldest first.
type pendingMessages struct {
	seq  int
	list []*pendingMessage
}

// pendingText reports whether msg is one that shows while it is on its
// way: text alone.
func pendingText(msg model.OutgoingMessage) bool {
	return msg.Files == nil && msg.Item == nil && msg.Path == "" && msg.Voice == nil && len(msg.Tasks) == 0
}

// addPending shows msg, sent to chat, at the end of its history until
// dropPending.
func (s *Store) addPending(chat int64, msg model.OutgoingMessage) *pendingMessage {
	me := s.Me()
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending.seq++
	p := &pendingMessage{chat: chat, seq: c.pending.seq}
	p.msg = model.Message{
		Key:              model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: model.MessageID(-(pendingIDBase + p.seq))},
		SenderID:         me.ID,
		SenderName:       me.Name(),
		Text:             msg.Text,
		Entities:         msg.Entities,
		Date:             time.Now(),
		Outgoing:         true,
		ReplyToMessageID: msg.ReplyTo,
		Sending:          true,
	}
	p.msg.ContentRevision = model.Revision(p.msg)
	c.pending.list = append(c.pending.list, p)
	c.touchPending(chat)
	return p
}

// sentPending notes that Telegram took p as message id. The caller holds
// c.mu.
func (c *conversation) sentPending(p *pendingMessage, id int) {
	p.id = model.MessageID(id)
	c.touchPending(p.chat)
}

// dropPending takes p off its history.
func (s *Store) dropPending(p *pendingMessage) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, q := range c.pending.list {
		if q == p {
			c.pending.list = append(c.pending.list[:i], c.pending.list[i+1:]...)
			c.touchPending(p.chat)
			return
		}
	}
}

// touchPending tells the history of chat that its messages on their way
// changed. The caller holds c.mu.
func (c *conversation) touchPending(chat int64) {
	if h := c.histories[chat]; h != nil {
		h.Revision++
	}
}

// pendingMessagesOf are the messages on their way that the history of
// chat, which holds have, shows at its end: those Telegram did not give
// one of have's ids yet. The caller holds c.mu.
func (c *conversation) pendingMessagesOf(chat int64, have []model.Message) []model.Message {
	var out []model.Message
	for _, p := range c.pending.list {
		if p.chat != chat || (p.id != 0 && holds(have, p.id)) {
			continue
		}
		out = append(out, p.msg)
	}
	return out
}

// holds reports whether messages, in the order of their ids, hold id;
// a message just sent is among the last.
func holds(messages []model.Message, id model.MessageID) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		switch got := messages[i].Key.MessageID; {
		case got == id:
			return true
		case got > 0 && got < id:
			return false
		}
	}
	return false
}

// sentID is the id Telegram gave the message sent as randomID, as res
// tells it, 0 when it does not.
func sentID(res tg.UpdatesClass, randomID int64) int {
	var all []tg.UpdateClass
	switch u := res.(type) {
	case *tg.UpdateShortSentMessage:
		return u.ID
	case *tg.Updates:
		all = u.Updates
	case *tg.UpdatesCombined:
		all = u.Updates
	case *tg.UpdateShort:
		all = []tg.UpdateClass{u.Update}
	}
	for _, u := range all {
		if u, ok := u.(*tg.UpdateMessageID); ok && u.RandomID == randomID {
			return u.ID
		}
	}
	return 0
}
