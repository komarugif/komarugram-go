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
)

// ghostState is what the account may tell, and what it told last.
type ghostState struct {
	mu     sync.Mutex
	ghost  model.Ghost
	read   map[int64]model.MessageID
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
	c := s.history
	c.mu.Lock()
	peer, top := c.peers[chat], c.top[chat]
	// What is left unread is counted from the history when it runs to the
	// chat's newest message; read up to the newest, nothing is.
	left := -1
	if int(id) >= top {
		left = 0
	} else if h := c.histories[chat]; h != nil && !h.HasNewer && len(h.Messages) > 0 && int(h.Messages[len(h.Messages)-1].Key.MessageID) >= top {
		left = 0
		for _, m := range h.Messages {
			if m.Key.MessageID > id && !m.Outgoing && !m.Local() {
				left++
			}
		}
	}
	c.mu.Unlock()
	if peer.ID == 0 {
		// Not marked as read: it is told once the peer is known.
		return
	}
	gs := &s.ghost
	gs.mu.Lock()
	if !asked && !gs.ghost.SendRead || gs.read[chat] >= id {
		gs.mu.Unlock()
		return
	}
	if gs.read == nil {
		gs.read = map[int64]model.MessageID{}
	}
	gs.read[chat] = id
	gs.mu.Unlock()
	if left >= 0 && s.lowerUnread(chat, left) {
		s.changed()
	}
	s.goSend("read", func(ctx context.Context, api *tg.Client) error {
		if peer.Kind == "channel" {
			_, err := api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, MaxID: int(id)})
			return err
		}
		_, err := api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: peer.input(), MaxID: int(id)})
		return err
	})
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

// goSend runs send with the API off the frame; what it tells is not worth
// a retry.
func (s *Store) goSend(what string, send func(context.Context, *tg.Client) error) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	api, ctx := c.api, c.ctx
	if api == nil || c.closing {
		return
	}
	c.wg.Go(func() {
		defer crash.Recover(what, nil)
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_ = send(ctx, api)
	})
}
