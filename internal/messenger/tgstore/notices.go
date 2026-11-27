// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// noticeAge is how old a message may be and still be told of: older ones
// come with the catch-up after a reconnection, and are not news.
const noticeAge = 10 * time.Minute

// SetNotices sets the function called, on the store's goroutine, with each
// new incoming message; nil stops it.
func (s *Store) SetNotices(f func(model.MessageNotice)) {
	if f == nil {
		s.notices.Store(nil)
		return
	}
	s.notices.Store(&f)
}

// notice tells of m, a new message of chat that came in raw.
func (s *Store) notice(m model.Message, chat model.Chat, raw tg.MessageClass) {
	f := s.notices.Load()
	if f == nil || chat.ID == 0 || m.Outgoing || m.Kind == model.MessageService || chat.Kind == model.KindSaved || time.Since(m.Date) > noticeAge || (chat.ReadInbox > 0 && m.Key.MessageID <= chat.ReadInbox) {
		return
	}
	n := model.MessageNotice{Chat: chat, Message: messagePreview(m, chat.Kind == model.KindGroup)}
	if r, ok := raw.(*tg.Message); ok {
		n.Silent = r.Silent
	}
	(*f)(n)
}

// notifyDefaults holds Telegram's account-wide default notification settings
// for private chats, groups, and channels.
type notifyDefaults struct {
	users      tg.PeerNotifySettings
	chats      tg.PeerNotifySettings
	broadcasts tg.PeerNotifySettings
}

func (d notifyDefaults) forKind(kind model.ChatKind) tg.PeerNotifySettings {
	switch kind {
	case model.KindGroup:
		return d.chats
	case model.KindChannel:
		return d.broadcasts
	default:
		return d.users
	}
}

// mutedNow tells whether settings mute their chat now.
func mutedNow(settings tg.PeerNotifySettings) bool {
	if until, ok := settings.GetMuteUntil(); ok {
		return until > 0 && time.Unix(int64(until), 0).After(time.Now())
	}
	if silent, ok := settings.GetSilent(); ok {
		return silent
	}
	return false
}

// mutedWithDefault resolves a chat's mute state from its per-peer settings,
// falling back to Telegram's scope default when the peer sets neither
// mute_until nor silent.
func mutedWithDefault(peer, def tg.PeerNotifySettings) bool {
	if until, ok := peer.GetMuteUntil(); ok {
		return until > 0 && time.Unix(int64(until), 0).After(time.Now())
	}
	if silent, ok := peer.GetSilent(); ok {
		return silent
	}
	return mutedNow(def)
}

// setMuted applies a change of a chat's notification settings.
func (s *Store) setMuted(id int64, settings tg.PeerNotifySettings) {
	s.mu.Lock()
	if s.notifyPeers == nil {
		s.notifyPeers = map[int64]tg.PeerNotifySettings{}
	}
	s.notifyPeers[id] = settings
	defs := s.notifyDefaults
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		if chats[i].ID == id {
			chats[i].Muted = mutedWithDefault(settings, defs.forKind(chats[i].Kind))
		}
	}
	s.chats = chats
	s.mu.Unlock()
	s.changed()
	s.goPersistDialogs()
}

// setDefaultNotify applies a change to Telegram's global default notification
// settings for private chats, groups, or channels.
func (s *Store) setDefaultNotify(peer tg.NotifyPeerClass, settings tg.PeerNotifySettings) {
	s.mu.Lock()
	switch peer.(type) {
	case *tg.NotifyUsers:
		s.notifyDefaults.users = settings
	case *tg.NotifyChats:
		s.notifyDefaults.chats = settings
	case *tg.NotifyBroadcasts:
		s.notifyDefaults.broadcasts = settings
	default:
		s.mu.Unlock()
		return
	}
	defs := s.notifyDefaults
	peers := s.notifyPeers
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		chats[i].Muted = mutedWithDefault(peers[chats[i].ID], defs.forKind(chats[i].Kind))
	}
	s.chats = chats
	s.mu.Unlock()
	s.changed()
	s.goPersistDialogs()
}
