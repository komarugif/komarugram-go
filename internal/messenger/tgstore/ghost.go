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
	// actionEvery is how often an action, typing or an upload, is told
	// again while it goes on, as Telegram Desktop's kSendMyTypingInterval.
	actionEvery = 5 * time.Second
	// actionOfflineFor is how long a user may be offline and still be told
	// what the account does, as Telegram Desktop's kSendTypingsToOfflineFor.
	actionOfflineFor = 30 * time.Second
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

// actionKey is an action in a chat.
type actionKey struct {
	chat   int64
	action model.ChatAction
}

// ghostState is what the account may tell, and what it told last.
type ghostState struct {
	mu    sync.Mutex
	ghost model.Ghost
	// read is what each chat was told, or is being told, it is read up
	// to; retry, until when a read that failed waits; again, until when
	// a read told already is not told again (see MarkRead).
	read  map[int64]model.MessageID
	retry map[int64]time.Time
	again map[int64]time.Time
	// acted is when each action was told last in each chat.
	acted  map[actionKey]time.Time
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

// SendAction implements model.GhostStore with messages.setTyping, told at
// most every actionEvery.
func (s *Store) SendAction(chat int64, action model.ChatAction) {
	s.sendAction(chat, action, 0)
}

// sendAction tells chat what the account does in it, an upload with how
// much of it is done, in percent, when Ghost allows, at most every
// actionEvery, and only where someone may see it: as Telegram Desktop, not
// in Saved Messages, a channel, a bot's chat, nor to a user offline for
// longer than actionOfflineFor.
func (s *Store) sendAction(chat int64, action model.ChatAction, progress int) {
	gs := &s.ghost
	now := time.Now()
	key := actionKey{chat, action}
	gs.mu.Lock()
	if !gs.ghost.Sends(action) || now.Sub(gs.acted[key]) < actionEvery {
		gs.mu.Unlock()
		return
	}
	if gs.acted == nil {
		gs.acted = map[actionKey]time.Time{}
	}
	gs.acted[key] = now
	gs.mu.Unlock()
	c := s.history
	c.mu.Lock()
	real, top := c.threadChat(chat)
	peer := c.peers[real]
	c.mu.Unlock()
	if peer.ID == 0 || !s.seesActions(real, peer, now) {
		return
	}
	input := inputAction(action, progress)
	if input == nil {
		return
	}
	s.goSend("action", func(ctx context.Context, api *tg.Client) error {
		req := &tg.MessagesSetTypingRequest{Peer: peer.input(), Action: input}
		if top != 0 {
			req.SetTopMsgID(top)
		}
		_, err := api.MessagesSetTyping(ctx, req)
		return err
	})
}

// seesActions reports whether peer, chat, would see what the account does
// in it, as Telegram Desktop's SendProgressManager decides: a user online
// a moment ago, or who hides when, but not oneself or a bot; a group, but
// not a channel.
func (s *Store) seesActions(chat int64, peer peerRecord, now time.Time) bool {
	switch {
	case peer.Rights.Self, peer.Rights.Bot:
		return false
	case peer.Kind == "channel":
		return !peer.Rights.Broadcast
	case peer.Kind != "user":
		return true
	}
	p, known := s.Presence(chat)
	switch {
	case !known, p.Seen == model.SeenRecently:
		return true
	case p.Seen == model.SeenAt:
		return !p.At.Before(now.Add(-actionOfflineFor))
	}
	return false
}

// inputAction is action as Telegram's, an upload with its progress; nil for
// none.
func inputAction(action model.ChatAction, progress int) tg.SendMessageActionClass {
	switch action {
	case model.ActionTyping:
		return &tg.SendMessageTypingAction{}
	case model.ActionRecordVoice:
		return &tg.SendMessageRecordAudioAction{}
	case model.ActionUploadVoice:
		return &tg.SendMessageUploadAudioAction{Progress: progress}
	case model.ActionRecordVideo:
		return &tg.SendMessageRecordVideoAction{}
	case model.ActionUploadVideo:
		return &tg.SendMessageUploadVideoAction{Progress: progress}
	case model.ActionRecordRound:
		return &tg.SendMessageRecordRoundAction{}
	case model.ActionUploadRound:
		return &tg.SendMessageUploadRoundAction{Progress: progress}
	case model.ActionUploadPhoto:
		return &tg.SendMessageUploadPhotoAction{Progress: progress}
	case model.ActionUploadFile:
		return &tg.SendMessageUploadDocumentAction{Progress: progress}
	case model.ActionChooseLocation:
		return &tg.SendMessageGeoLocationAction{}
	case model.ActionChooseContact:
		return &tg.SendMessageChooseContactAction{}
	case model.ActionChooseSticker:
		return &tg.SendMessageChooseStickerAction{}
	case model.ActionPlayGame:
		return &tg.SendMessageGamePlayAction{}
	}
	return nil
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
