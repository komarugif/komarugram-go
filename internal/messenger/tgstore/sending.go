// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"maps"
	"slices"
	"sort"
	"time"

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
	// scheduled keeps a ghost-scheduled message visible in pending until
	// UpdateDeleteScheduledMessages arrives or scheduledUntil passes.
	scheduled      bool
	scheduledUntil time.Time
}

// pendingMessages are the account's messages on their way, oldest first.
type pendingMessages struct {
	seq  int
	list []*pendingMessage
	// Only one album can be in messages.sendMultiMedia at a time.
	album *pendingAlbum
}

type pendingAlbum struct {
	chat, peer int64
	after      model.MessageID
	group      int64
	parts      []pendingAlbumPart
}

type pendingAlbumPart struct {
	pending *pendingMessage
	mediaID int64
	photo   bool
}

// trackPendingAlbum remembers the media IDs assigned by uploadMedia. The
// corresponding new messages can arrive before sendMultiMedia returns their
// message IDs.
func (c *conversation) trackPendingAlbum(e *outgoingEntry, order []int, items []tg.InputSingleMedia) {
	album := &pendingAlbum{chat: e.Chat, peer: e.Peer.key()}
	for i, item := range items {
		part := pendingAlbumPart{pending: e.pending[order[i]]}
		switch media := item.Media.(type) {
		case *tg.InputMediaPhoto:
			if ref, ok := media.ID.(*tg.InputPhoto); ok {
				part.mediaID, part.photo = ref.ID, true
			}
		case *tg.InputMediaDocument:
			if ref, ok := media.ID.(*tg.InputDocument); ok {
				part.mediaID = ref.ID
			}
		}
		if part.mediaID == 0 {
			return
		}
		album.parts = append(album.parts, part)
	}
	c.mu.Lock()
	album.after = model.MessageID(c.top[album.peer])
	c.pending.album = album
	c.mu.Unlock()
}

func (c *conversation) untrackPendingAlbum() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if album := c.pending.album; album != nil {
		c.touchPending(album.chat)
		c.pending.album = nil
	}
}

// notePendingAlbumPart binds a live update to its pending upload, even in
// Saved Messages, where the server leaves Out unset. The caller holds c.mu.
func (c *conversation) notePendingAlbumPart(m model.Message, ref *fileLocation, own bool) {
	album := c.pending.album
	if album == nil || !own || ref == nil || m.Key.ChatID != album.peer ||
		m.Key.MessageID <= album.after || m.GroupedID == 0 || album.group != 0 && album.group != m.GroupedID {
		return
	}
	for _, part := range album.parts {
		if part.pending.id == m.Key.MessageID {
			return
		}
	}
	for _, part := range album.parts {
		if part.pending.id == 0 && part.mediaID == ref.ID && part.photo == ref.Photo {
			album.group = m.GroupedID
			c.sentPending(part.pending, int(m.Key.MessageID))
			return
		}
	}
}

// pendingAlbumContains reports whether this message confirms a tile of the
// album being sent. The caller holds c.mu.
func (c *conversation) pendingAlbumContains(m model.Message) bool {
	album := c.pending.album
	return album != nil && album.peer == m.Key.ChatID && m.GroupedID != 0 &&
		slices.ContainsFunc(album.parts, func(part pendingAlbumPart) bool {
			return part.pending.id == m.Key.MessageID
		})
}

// shownServerMessages hides an incomplete confirmation while all of its
// pending tiles are still available. The caller holds c.mu.
func (c *conversation) shownServerMessages(chat int64, have []model.Message) []model.Message {
	out := append([]model.Message(nil), have...)
	album := c.pending.album
	if album == nil || album.chat != chat {
		return out
	}
	var confirmed []model.MessageID
	for _, part := range album.parts {
		if id := part.pending.id; id != 0 && holds(have, id) {
			confirmed = append(confirmed, id)
		}
	}
	if len(confirmed) == 0 || len(confirmed) == len(album.parts) {
		return out
	}
	return slices.DeleteFunc(out, func(m model.Message) bool {
		return slices.Contains(confirmed, m.Key.MessageID)
	})
}

// sentPending notes that Telegram took p as message id. The caller holds
// c.mu.
func (c *conversation) sentPending(p *pendingMessage, id int) {
	p.id = model.MessageID(id)
	c.touchPending(p.chat)
}

// dropPending removes the specified pending rows in one history change.
func (s *Store) dropPending(pending ...*pendingMessage) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending.list = slices.DeleteFunc(c.pending.list, func(p *pendingMessage) bool {
		if slices.Contains(pending, p) {
			c.touchPending(p.chat)
			return true
		}
		return false
	})
}

// markPendingScheduled keeps pending entries visible while a ghost-scheduled
// message waits for Telegram to deliver it.
func (s *Store) markPendingScheduled(pending []*pendingMessage, scheduleDate int) {
	if scheduleDate <= 0 || len(pending) == 0 {
		return
	}
	until := time.Unix(int64(scheduleDate), 0).Add(ghostScheduleGrace)
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range pending {
		p.scheduled = true
		p.scheduledUntil = until
		c.touchPending(p.chat)
	}
}

// dropFinishedPending removes completed non-scheduled pending rows.
func (s *Store) dropFinishedPending(pending ...*pendingMessage) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending.list = slices.DeleteFunc(c.pending.list, func(p *pendingMessage) bool {
		if !p.scheduled && slices.Contains(pending, p) {
			c.touchPending(p.chat)
			return true
		}
		return false
	})
}

// dropScheduledPending removes ghost-scheduled pending rows once Telegram
// reports them delivered or deleted.
func (s *Store) dropScheduledPending(chat int64, ids []int) bool {
	if len(ids) == 0 {
		return false
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := false
	c.pending.list = slices.DeleteFunc(c.pending.list, func(p *pendingMessage) bool {
		if !p.scheduled || !slices.Contains(ids, int(p.id)) {
			return false
		}
		real, _ := c.threadChat(p.chat)
		if real != chat {
			return false
		}
		c.touchPending(p.chat)
		removed = true
		return true
	})
	return removed
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
	now := time.Now()
	for _, p := range c.pending.list {
		if p.chat != chat || (p.id != 0 && holds(have, p.id)) {
			continue
		}
		if p.scheduled && !p.scheduledUntil.IsZero() && now.After(p.scheduledUntil) {
			continue
		}
		out = append(out, p.msg)
	}
	return out
}

// shownMessages merges device-only stickers and messages that failed by their
// time, keeping them outside Telegram's ID-ordered history and its cache spans.
// A message on its way comes last: Telegram will place it after what is there.
func (c *conversation) shownMessages(chat int64, h *model.History) []model.Message {
	have := c.shownServerMessages(chat, h.Messages)
	out := have
	// Beyond what is loaded, it shows when that part of the history does.
	loaded := func(m model.Message) bool {
		return len(h.Messages) == 0 || !(h.HasOlder && m.Date.Before(h.Messages[0].Date) || h.HasNewer && m.Date.After(h.Messages[len(h.Messages)-1].Date))
	}
	for _, m := range c.localStickers {
		if m.Key.ChatID != chat || !c.showLocalStickers {
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
		if loaded(m) {
			out = append(out, m)
		}
	}
	var onItsWay []model.Message
	for _, m := range c.pendingMessagesOf(chat, have) {
		switch {
		case m.SendError == "":
			onItsWay = append(onItsWay, m)
		case loaded(m):
			out = append(out, m)
		}
	}
	if len(out) > len(have) {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	}
	return append(out, onItsWay...)
}

func (s *Store) withOutgoingPreviews(chats []model.Chat) []model.Chat {
	c := s.history
	c.mu.Lock()
	if len(c.pending.list) == 0 && (len(c.localStickers) == 0 || !c.showLocalStickers) {
		c.mu.Unlock()
		return chats
	}
	last := map[int64]model.Message{}
	for _, m := range c.localStickers {
		if c.showLocalStickers && !isThread(m.Key.ChatID) && !m.Date.Before(last[m.Key.ChatID].Date) {
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
	// Asked in frames: the slice is the same until something in it changes.
	if sameSlice(c.previewFrom, chats) && maps.EqualFunc(c.previewLast, last, samePreview) {
		out := c.preview
		c.mu.Unlock()
		return out
	}
	peers := map[int64]peerRecord{}
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
	c.mu.Lock()
	c.previewFrom, c.previewLast, c.preview = chats, last, out
	c.mu.Unlock()
	return out
}

// samePreview reports whether a and b, last messages of a chat, preview alike.
func samePreview(a, b model.Message) bool {
	return a.Key == b.Key && a.Date.Equal(b.Date) && a.Sending == b.Sending && a.SendError == b.SendError &&
		a.LocalPremium == b.LocalPremium && a.ContentRevision == b.ContentRevision
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
