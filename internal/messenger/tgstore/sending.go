// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"sort"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// Outgoing messages stand in for their eventual Telegram IDs. Durable
// requests and their lifetime are managed by the account's outbox.

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

// shownMessages merges device-only stickers by their time, keeping them outside
// Telegram's ID-ordered history and its cache spans.
func (c *conversation) shownMessages(chat int64, h *model.History) []model.Message {
	out := append([]model.Message(nil), h.Messages...)
	for _, m := range c.localStickers {
		if m.Key.ChatID != chat {
			continue
		}
		queued := false
		for _, p := range c.pending.list {
			if p.msg.Key == m.Key {
				queued = true
				break
			}
		}
		if queued {
			continue
		}
		if len(h.Messages) > 0 && (h.HasOlder && m.Date.Before(h.Messages[0].Date) || h.HasNewer && m.Date.After(h.Messages[len(h.Messages)-1].Date)) {
			continue
		}
		out = append(out, m)
	}
	if len(out) > len(h.Messages) {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	}
	return append(out, c.pendingMessagesOf(chat, h.Messages)...)
}

func (s *Store) withOutgoingPreviews(chats []model.Chat) []model.Chat {
	c := s.history
	c.mu.Lock()
	if len(c.pending.list) == 0 && len(c.localStickers) == 0 {
		c.mu.Unlock()
		return chats
	}
	last := map[int64]model.Message{}
	peers := map[int64]peerRecord{}
	for _, m := range c.localStickers {
		if !isThread(m.Key.ChatID) && !m.Date.Before(last[m.Key.ChatID].Date) {
			last[m.Key.ChatID] = m
		}
	}
	for _, p := range c.pending.list {
		real, _ := c.threadChat(p.chat)
		if real == 0 {
			continue
		}
		if !p.msg.Date.Before(last[real].Date) {
			last[real] = p.msg
		}
	}
	for chat := range last {
		peers[chat] = c.peers[chat]
	}
	c.mu.Unlock()
	out := append([]model.Chat(nil), chats...)
	for id, m := range last {
		index := -1
		for i := range out {
			if out[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			out = append(out, peers[id].withMetadata(model.Chat{ID: id}))
			index = len(out) - 1
		}
		if m.Date.Before(out[index].LastTime) {
			continue
		}
		setPreview(&out[index], m)
		out[index].Last.Sending = m.Sending
		out[index].Last.SendFailed = m.SendError != ""
		if m.LocalPremium && m.Key.MessageID < 0 {
			out[index].Last.Sent = false
		}
	}
	model.SortChats(out)
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
