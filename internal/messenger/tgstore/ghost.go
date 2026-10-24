// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

const (
	// typingEvery is how often typing is told again while it goes on, as
	// Telegram Desktop does.
	typingEvery = 5 * time.Second
	// onlineEvery keeps the account online: Telegram shows it offline a
	// few minutes after the last status.
	onlineEvery = 2 * time.Minute
	// readRetry is how long a read Telegram was not told waits to be told
	// again.
	readRetry = 5 * time.Second
	// readAgain is how often a chat read to its end whose unread count
	// came back is told again that it is read.
	readAgain = 10 * time.Second
)

// ghostState is what the account may tell, and what it told last.
type ghostState struct {
	mu    sync.Mutex
	ghost model.Ghost
	// read is what each chat was told, or is being told, it is read up
	// to; retry, until when a read that failed waits; again, until when
	// a read told already is not told again (see MarkRead).
	read   map[int64]model.MessageID
	retry  map[int64]time.Time
	again  map[int64]time.Time
	typed  map[int64]time.Time
	online bool
	// told is when the status was sent last; toldOnline, which one.
	told       time.Time
	toldOnline bool
}

// SetGhost implements model.GhostStore. Turning online off while online
// tells Telegram the account went offline.
func (s *Store) SetGhost(g model.Ghost) {
	gs := &s.ghost
	gs.mu.Lock()
	was := gs.ghost
	gs.ghost = g
	offline := was.SendOnline && !g.SendOnline && gs.toldOnline
	if offline {
		gs.toldOnline, gs.told = false, time.Now()
	}
	gs.mu.Unlock()
	if offline {
		s.goSend("status", func(ctx context.Context, api *tg.Client) error {
			_, err := api.AccountUpdateStatus(ctx, true)
			return err
		})
	}
}

// Ghost implements model.GhostStore.
func (s *Store) Ghost() model.Ghost { return s.ghostSettings() }

func (s *Store) ghostSettings() model.Ghost {
	s.ghost.mu.Lock()
	defer s.ghost.mu.Unlock()
	return s.ghost.ghost
}

// MarkRead implements model.GhostStore with messages.readHistory, or
// channels.readHistory in a channel. Each message is read once.
func (s *Store) MarkRead(chat int64, id model.MessageID, asked bool) {
	if id <= 0 {
		return
	}
	if isThread(chat) {
		s.markTopicRead(chat, id, asked)
		return
	}
	// The messages read before: those after them up to id are read now.
	prev, unread := s.readUpTo(chat), 0
	for _, ch := range s.Chats() {
		if ch.ID == chat {
			prev, unread = max(prev, ch.ReadInbox), ch.Unread
		}
	}
	c := s.history
	c.mu.Lock()
	peer, top := c.peers[chat], c.top[chat]
	// What is left unread is counted from the history when it runs to the
	// chat's newest message; read up to the newest, nothing is. Short of
	// it, the count goes down by the messages read now that the history
	// has: Telegram tells this session nothing of its own reads.
	left, read := -1, 0
	if int(id) >= top {
		left = 0
	} else if h := c.histories[chat]; h != nil && !h.HasNewer && len(h.Messages) > 0 && int(h.Messages[len(h.Messages)-1].Key.MessageID) >= top {
		left = 0
		for _, m := range h.Messages {
			if m.Key.MessageID > id && !m.Outgoing && !m.Local() {
				left++
			}
		}
	} else if h != nil {
		for _, m := range h.Messages {
			if m.Key.MessageID > prev && m.Key.MessageID <= id && !m.Outgoing && !m.Local() {
				read++
			}
		}
	}
	c.mu.Unlock()
	if peer.ID == 0 {
		// Not marked as read: it is told once the peer is known.
		return
	}
	// Read to its end, the chat has nothing unread, whatever brought its
	// count back after the read was told: the count goes, and the read is
	// told again, at most every readAgain.
	stuck := left == 0 && unread > 0
	gs := &s.ghost
	gs.mu.Lock()
	allowed := asked || gs.ghost.SendRead
	back := allowed && stuck && gs.read[chat] >= id
	tell := gs.mayRead(chat, id, asked)
	if !tell && back && !time.Now().Before(gs.again[chat]) {
		tell = true
		if gs.again == nil {
			gs.again = map[int64]time.Time{}
		}
		gs.again[chat] = time.Now().Add(readAgain)
	}
	if tell {
		if gs.read == nil {
			gs.read = map[int64]model.MessageID{}
		}
		gs.read[chat] = max(gs.read[chat], id)
	}
	gs.mu.Unlock()
	if !tell && !(allowed && stuck) {
		return
	}
	if s.markInbox(chat, id, left, read) {
		if back {
			// Seen in -profile: what came back is worth finding.
			s.profileHistory("read.count-came-back", chat, 0, int(id), unread, time.Time{}, nil)
		}
		s.changed()
	}
	if !tell {
		return
	}
	s.tellRead(chat, id, func(ctx context.Context, api *tg.Client) error {
		if peer.Kind == "channel" {
			_, err := api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, MaxID: int(id)})
			return err
		}
		_, err := api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: peer.input(), MaxID: int(id)})
		return err
	})
}

// mayRead reports whether chat is to be told it is read up to id: Ghost
// allows it or the user asked, it was not told that already, and no read
// that failed waits. The caller holds gs.mu.
func (gs *ghostState) mayRead(chat int64, id model.MessageID, asked bool) bool {
	return (asked || gs.ghost.SendRead) && gs.read[chat] < id && !time.Now().Before(gs.retry[chat])
}

// tellRead tells Telegram with send that chat is read up to id. A read not
// told, without a connection or on an error, is forgotten for the history
// to tell again, after readRetry on an error: kept as told, it never would
// be, and the dialogs loaded next would bring the chat's unread count back
// for good.
func (s *Store) tellRead(chat int64, id model.MessageID, send func(context.Context, *tg.Client) error) {
	forget := func(failed bool) {
		gs := &s.ghost
		gs.mu.Lock()
		if gs.read[chat] == id {
			delete(gs.read, chat)
		}
		if failed {
			if gs.retry == nil {
				gs.retry = map[int64]time.Time{}
			}
			gs.retry[chat] = time.Now().Add(readRetry)
		}
		gs.mu.Unlock()
	}
	sent := s.goSend("read", func(ctx context.Context, api *tg.Client) error {
		err := send(ctx, api)
		if err != nil {
			forget(true)
			// Drawn again then, the history tells it again.
			time.AfterFunc(readRetry, s.changed)
		}
		return err
	})
	// Without a connection yet the history tells it again once there is
	// one: the dialogs it loads draw it again.
	if !sent {
		forget(false)
	}
}

// readOnInteract marks chat read up to its last message after the account
// sent to it or reacted in it, when Ghost.ReadOnInteract asks for that.
func (s *Store) readOnInteract(chat int64) {
	g := s.ghostSettings()
	if g.SendRead || !g.ReadOnInteract {
		return
	}
	chat = s.realChat(chat)
	c := s.history
	c.mu.Lock()
	top := c.top[chat]
	c.mu.Unlock()
	s.MarkRead(chat, model.MessageID(top), true)
}

// SetOnline implements model.GhostStore: while Ghost.SendOnline, the
// account is online while a window is active, and told so again every
// onlineEvery; offline when none is.
func (s *Store) SetOnline(online bool) {
	gs := &s.ghost
	gs.mu.Lock()
	gs.online = online
	send := gs.ghost.SendOnline && (online != gs.toldOnline || online && time.Since(gs.told) > onlineEvery)
	if send {
		gs.toldOnline, gs.told = online, time.Now()
	}
	gs.mu.Unlock()
	if send {
		s.goSend("status", func(ctx context.Context, api *tg.Client) error {
			_, err := api.AccountUpdateStatus(ctx, !online)
			return err
		})
	}
}

// Typing implements model.GhostStore with messages.setTyping, told at most
// every typingEvery.
func (s *Store) Typing(chat int64) {
	gs := &s.ghost
	gs.mu.Lock()
	if !gs.ghost.SendTyping || time.Since(gs.typed[chat]) < typingEvery {
		gs.mu.Unlock()
		return
	}
	if gs.typed == nil {
		gs.typed = map[int64]time.Time{}
	}
	gs.typed[chat] = time.Now()
	gs.mu.Unlock()
	c := s.history
	c.mu.Lock()
	real, top := c.threadChat(chat)
	peer := c.peers[real]
	c.mu.Unlock()
	if peer.ID == 0 {
		return
	}
	s.goSend("typing", func(ctx context.Context, api *tg.Client) error {
		req := &tg.MessagesSetTypingRequest{Peer: peer.input(), Action: &tg.SendMessageTypingAction{}}
		if top != 0 {
			req.SetTopMsgID(top)
		}
		_, err := api.MessagesSetTyping(ctx, req)
		return err
	})
}

// goSend runs send with the API off the frame, and reports whether it did:
// not without a connection. What it tells is not worth a retry, but reads
// (tellRead).
func (s *Store) goSend(what string, send func(context.Context, *tg.Client) error) bool {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	api, ctx := c.api, c.ctx
	if api == nil || c.closing {
		return false
	}
	c.wg.Go(func() {
		defer crash.Recover(what, nil)
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_ = send(ctx, api)
	})
	return true
}
