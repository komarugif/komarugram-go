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

// presenceRefresh is how long a user's presence asked for is not asked for
// again.
const presenceRefresh = time.Minute

// presences are when users were online, by the ids of their chats, as
// Telegram tells it with the users it sends and in updateUserStatus. The
// cache keeps none of it: a presence read offline would be old.
type presences struct {
	mu    sync.Mutex
	known map[int64]model.Presence
	asked map[int64]time.Time
}

// userPresence is the presence Telegram's status tells, false for none.
func userPresence(status tg.UserStatusClass) (model.Presence, bool) {
	switch s := status.(type) {
	case *tg.UserStatusOnline:
		return model.Presence{Seen: model.SeenAt, At: time.Unix(int64(s.Expires), 0)}, true
	case *tg.UserStatusOffline:
		return model.Presence{Seen: model.SeenAt, At: time.Unix(int64(s.WasOnline), 0)}, true
	case *tg.UserStatusRecently:
		return model.Presence{Seen: model.SeenRecently}, true
	case *tg.UserStatusLastWeek:
		return model.Presence{Seen: model.SeenLastWeek}, true
	case *tg.UserStatusLastMonth:
		return model.Presence{Seen: model.SeenLastMonth}, true
	case *tg.UserStatusEmpty:
		return model.Presence{Seen: model.SeenLongAgo}, true
	}
	return model.Presence{}, false
}

// setPresence keeps the presence of user that status tells, if it tells
// one.
func (s *Store) setPresence(user int64, status tg.UserStatusClass) {
	p, ok := userPresence(status)
	if !ok {
		return
	}
	id := peerID(&tg.PeerUser{UserID: user})
	s.presence.mu.Lock()
	defer s.presence.mu.Unlock()
	if s.presence.known == nil {
		s.presence.known = map[int64]model.Presence{}
	}
	s.presence.known[id] = p
}

func (s *Store) Presence(chat int64) (model.Presence, bool) {
	s.presence.mu.Lock()
	defer s.presence.mu.Unlock()
	p, ok := s.presence.known[chat]
	return p, ok
}

// RefreshPresence asks Telegram for the user of chat with users.getUsers,
// which only reads.
func (s *Store) RefreshPresence(chat int64) {
	now := time.Now()
	s.presence.mu.Lock()
	if now.Sub(s.presence.asked[chat]) < presenceRefresh {
		s.presence.mu.Unlock()
		return
	}
	if s.presence.asked == nil {
		s.presence.asked = map[int64]time.Time{}
	}
	s.presence.asked[chat] = now
	s.presence.mu.Unlock()
	c := s.history
	c.mu.Lock()
	api, peer, ctx := c.api, c.peers[chat], c.ctx
	c.mu.Unlock()
	if api == nil || peer.Kind != "user" || peer.Rights.Self || ctx == nil {
		return
	}
	go func() {
		defer crash.Recover("presence", nil)
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: peer.ID, AccessHash: peer.Hash}})
		if err != nil || len(users) == 0 {
			return
		}
		s.rememberPeers(users, nil)
		s.changed()
	}()
}
