// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A bot streams a message as it writes it (live message streaming,
// https://core.telegram.org/api/bots/ai#live-message-streaming): typing
// updates carry the text so far (sendMessageTextDraftAction) or the article
// (sendMessageRichMessageDraftAction), the updates of one message under one
// random id. Each is shown as Telegram Desktop shows it
// (HistoryStreamedDrafts): a message at the end of its chat's history, and
// of its thread's, which the next draft of its sender in the thread
// replaces; it goes when the message it becomes comes, when the bot stops
// it, and after draftTimeout without news. The cache keeps none of it.

// draftTimeout is how long a draft stays without news, as Telegram
// Desktop's kClearTimeout.
const draftTimeout = 30 * time.Second

// streamedDraft is a message a bot streams.
type streamedDraft struct {
	// chat is the chat it is in, top the root of its thread there, 0 for
	// none, and from the bot.
	chat, from int64
	top        int
	// seq orders the drafts, and makes their message ids.
	seq int
	// msg is the message as the draft has it so far.
	msg                 model.Message
	canStop, keepOnStop bool
	updated             time.Time
}

// streamedDrafts are the drafts streamed to the account, by random id.
// stopped are those stopped, whose news is dropped.
type streamedDrafts struct {
	byID    map[int64]*streamedDraft
	stopped map[int64]bool
	seq     int
	timer   *time.Timer
}

// applyDraftAction applies a typing update of from in chat, a peer, in the
// thread of root top: a draft, its stop, or anything else, which it
// ignores.
func (s *Store) applyDraftAction(peer tg.PeerClass, top int, from tg.PeerClass, action tg.SendMessageActionClass) {
	user, ok := from.(*tg.PeerUser)
	if !ok || user.UserID == s.Me().ID {
		return
	}
	msg := &tg.Message{PeerID: peer, FromID: from, Date: int(time.Now().Unix())}
	if top != 0 {
		msg.SetReplyTo(&tg.MessageReplyHeader{ReplyToMsgID: top})
	}
	var canStop, keepOnStop bool
	var randomID int64
	switch a := action.(type) {
	case *tg.SendMessageTextDraftAction:
		msg.Message, msg.Entities = a.Text.Text, a.Text.Entities
		canStop, keepOnStop, randomID = a.CanStop, a.KeepOnStop, a.RandomID
	case *tg.SendMessageRichMessageDraftAction:
		msg.SetRichMessage(a.RichMessage)
		canStop, keepOnStop, randomID = a.CanStop, a.KeepOnStop, a.RandomID
	case *tg.SendMessageStopDraftAction:
		s.stopDraft(a.RandomID)
		return
	default:
		return
	}
	s.putDraft(peerID(peer), top, peerID(from), randomID, msg, canStop, keepOnStop)
}

// putDraft keeps what draft randomID of from in chat, in the thread of root
// top, has so far: msg.
func (s *Store) putDraft(chat int64, top int, from, randomID int64, msg *tg.Message, canStop, keepOnStop bool) {
	c := s.history
	c.mu.Lock()
	if c.drafts.stopped[randomID] {
		c.mu.Unlock()
		return
	}
	names := make(map[int64]string, len(c.peers))
	for id, p := range c.peers {
		names[id] = p.Name
	}
	m, _ := convertMessage(c.account, msg, names)
	// The article's media download as a message's do.
	if rich, ok := msg.GetRichMessage(); ok {
		refs := map[string]fileLocation{}
		richRefs(rich, refs)
		for id, ref := range refs {
			c.refs[id] = ref
		}
	}
	d := c.drafts.byID[randomID]
	if d == nil {
		// The next draft of a sender in a thread replaces the last one.
		for id, old := range c.drafts.byID {
			if old.chat == chat && old.top == top && old.from == from {
				delete(c.drafts.byID, id)
			}
		}
		if c.drafts.byID == nil {
			c.drafts.byID = map[int64]*streamedDraft{}
		}
		c.drafts.seq++
		d = &streamedDraft{chat: chat, top: top, from: from, seq: c.drafts.seq}
		c.drafts.byID[randomID] = d
	}
	d.msg, d.canStop, d.keepOnStop, d.updated = m, canStop, keepOnStop, time.Now()
	c.touchDrafts(chat, top)
	s.scheduleDraftCheck()
	c.mu.Unlock()
	s.changed()
}

// stopDraft stops draft randomID: it goes, unless it keeps on stop.
func (s *Store) stopDraft(randomID int64) {
	c := s.history
	c.mu.Lock()
	if c.drafts.stopped == nil {
		c.drafts.stopped = map[int64]bool{}
	}
	c.drafts.stopped[randomID] = true
	d := c.drafts.byID[randomID]
	if d == nil {
		c.mu.Unlock()
		return
	}
	if d.keepOnStop {
		d.canStop = false
	} else {
		delete(c.drafts.byID, randomID)
	}
	c.touchDrafts(d.chat, d.top)
	c.mu.Unlock()
	s.changed()
}

// adoptDraft drops the draft that new message m of a bot becomes: the
// sender's in m's chat, in the thread m is in or in none. A message without
// a sender is the chat's own, as in Telegram Desktop.
func (s *Store) adoptDraft(m model.Message) {
	from := m.SenderID
	if from == 0 {
		from = m.Key.ChatID
	}
	c := s.history
	c.mu.Lock()
	adopted := false
	for id, d := range c.drafts.byID {
		if d.chat != m.Key.ChatID || d.from != from {
			continue
		}
		if d.top == 0 || d.top == int(m.ReplyToTopID) || d.top == int(m.ReplyToMessageID) {
			delete(c.drafts.byID, id)
			c.touchDrafts(d.chat, d.top)
			adopted = true
		}
	}
	c.mu.Unlock()
	if adopted {
		s.changed()
	}
}

// StopStreamedDraft implements model.DraftStopper with messages.setTyping:
// it stops the newest draft of chat, or of the thread chat is, that the
// account may stop.
func (s *Store) StopStreamedDraft(chat int64) error {
	c := s.history
	c.mu.Lock()
	real, root := c.threadChat(chat)
	var newest *streamedDraft
	var randomID int64
	for id, d := range c.drafts.byID {
		if d.chat != real || !d.canStop || isThread(chat) && d.top != root {
			continue
		}
		if newest == nil || d.updated.After(newest.updated) {
			newest, randomID = d, id
		}
	}
	peer := c.peers[real]
	c.mu.Unlock()
	if newest == nil || peer.ID == 0 {
		return errors.New("no draft to stop")
	}
	top := newest.top
	s.goSend("stop draft", func(ctx context.Context, api *tg.Client) error {
		req := &tg.MessagesSetTypingRequest{Peer: peer.input(), Action: &tg.SendMessageStopDraftAction{RandomID: randomID}}
		if top != 0 {
			req.SetTopMsgID(top)
		}
		_, err := api.MessagesSetTyping(ctx, req)
		return err
	})
	s.stopDraft(randomID)
	return nil
}

// scheduleDraftCheck drops the drafts without news for draftTimeout, then.
// The caller holds c.mu.
func (s *Store) scheduleDraftCheck() {
	c := s.history
	if c.drafts.timer != nil {
		return
	}
	c.drafts.timer = time.AfterFunc(draftTimeout, s.expireDrafts)
}

// expireDrafts drops the drafts without news for draftTimeout, and checks
// again when the next of them would be.
func (s *Store) expireDrafts() {
	c := s.history
	c.mu.Lock()
	c.drafts.timer = nil
	now := time.Now()
	var next time.Time
	expired := false
	for id, d := range c.drafts.byID {
		if due := d.updated.Add(draftTimeout); !now.Before(due) {
			delete(c.drafts.byID, id)
			c.touchDrafts(d.chat, d.top)
			expired = true
		} else if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	if !next.IsZero() {
		c.drafts.timer = time.AfterFunc(next.Sub(now), s.expireDrafts)
	}
	c.mu.Unlock()
	if expired {
		s.changed()
	}
}

// touchDrafts tells the histories a draft of chat, in the thread of root
// top, shows in that it changed. The caller holds c.mu.
func (c *conversation) touchDrafts(chat int64, top int) {
	if h := c.histories[chat]; h != nil {
		h.Revision++
	}
	for id, t := range c.threads {
		if t.group == chat && top != 0 && t.top == top {
			if h := c.histories[id]; h != nil {
				h.Revision++
			}
		}
	}
}

// draftMessages are the drafts the history of chat shows at its end: all
// of a chat's, and a thread's own. The caller holds c.mu.
func (c *conversation) draftMessages(chat int64) []model.Message {
	real, root := c.threadChat(chat)
	var drafts []*streamedDraft
	for _, d := range c.drafts.byID {
		if d.chat == real && (!isThread(chat) || d.top == root) {
			drafts = append(drafts, d)
		}
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].seq < drafts[j].seq })
	out := make([]model.Message, 0, len(drafts))
	for _, d := range drafts {
		m := d.msg
		m.Key = model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: model.MessageID(-d.seq)}
		if isThread(chat) {
			// In its thread, a draft replies to nothing: all of it does.
			m.ReplyToMessageID = 0
		}
		m.Streaming, m.Stoppable = true, d.canStop
		m.ContentRevision = model.Revision(m)
		out = append(out, m)
	}
	return out
}
