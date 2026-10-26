// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// ResolveUsername implements model.TelegramLinkSource: a peer known by the
// name first, then Telegram's, as Telegram Desktop.
func (s *Store) ResolveUsername(ctx context.Context, name string) (model.Chat, error) {
	c := s.history
	c.mu.Lock()
	api, id := c.api, int64(0)
	for pid, p := range c.peers {
		if p.Username != "" && strings.EqualFold(p.Username, name) {
			id = pid
			break
		}
	}
	c.mu.Unlock()
	if id != 0 {
		return s.chatFor(id, nil), nil
	}
	if api == nil {
		return model.Chat{}, errors.New("offline")
	}
	res, err := s.resolveUsername(ctx, api, name)
	if err != nil {
		return model.Chat{}, err
	}
	return s.chatFor(peerID(res.Peer), nil), nil
}

// resolveUsername asks Telegram whose name it is, and remembers them.
func (s *Store) resolveUsername(ctx context.Context, api *tg.Client, name string) (*tg.ContactsResolvedPeer, error) {
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: name})
	if tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID") {
		return nil, model.ErrLinkNotFound
	}
	if err != nil {
		return nil, err
	}
	s.rememberPeers(res.Users, res.Chats)
	return res, nil
}

// ChannelByID implements model.TelegramLinkSource for t.me/c links.
func (s *Store) ChannelByID(ctx context.Context, id int64) (model.Chat, error) {
	chat := peerID(&tg.PeerChannel{ChannelID: id})
	c := s.history
	c.mu.Lock()
	peer, known := c.peers[chat]
	api := c.api
	c.mu.Unlock()
	if known && peer.Hash != 0 {
		return s.chatFor(chat, nil), nil
	}
	if api == nil {
		return model.Chat{}, errors.New("offline")
	}
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: id}})
	if err != nil {
		return model.Chat{}, model.ErrLinkNotFound
	}
	for _, raw := range res.GetChats() {
		if ch, ok := raw.(*tg.Channel); ok && ch.ID == id && !ch.Min {
			s.rememberPeers(nil, []tg.ChatClass{ch})
			return s.chatFor(chat, nil), nil
		}
	}
	return model.Chat{}, model.ErrLinkNotFound
}

// CheckInvite implements model.TelegramLinkSource.
func (s *Store) CheckInvite(ctx context.Context, hash string) (model.ChatInvite, error) {
	api := s.api()
	if api == nil {
		return model.ChatInvite{}, errors.New("offline")
	}
	res, err := api.MessagesCheckChatInvite(ctx, hash)
	if tgerr.Is(err, "INVITE_HASH_EXPIRED", "INVITE_HASH_INVALID", "INVITE_HASH_EMPTY") {
		return model.ChatInvite{}, model.ErrLinkNotFound
	}
	if err != nil {
		return model.ChatInvite{}, err
	}
	switch r := res.(type) {
	case *tg.ChatInviteAlready:
		return s.inviteChat(r.Chat)
	case *tg.ChatInvitePeek:
		return s.inviteChat(r.Chat)
	case *tg.ChatInvite:
		return model.ChatInvite{Title: r.Title, Members: r.ParticipantsCount, Channel: r.Broadcast, Request: r.RequestNeeded, Paid: r.SubscriptionFormID != 0}, nil
	}
	return model.ChatInvite{}, model.ErrLinkNotFound
}

func (s *Store) inviteChat(raw tg.ChatClass) (model.ChatInvite, error) {
	s.rememberPeers(nil, []tg.ChatClass{raw})
	var p tg.PeerClass
	switch ch := raw.(type) {
	case *tg.Channel:
		p = &tg.PeerChannel{ChannelID: ch.ID}
	case *tg.Chat:
		p = &tg.PeerChat{ChatID: ch.ID}
	default:
		return model.ChatInvite{}, model.ErrLinkNotFound
	}
	chat := s.chatFor(peerID(p), nil)
	return model.ChatInvite{Chat: &chat, Title: chat.Title}, nil
}

// JoinInvite implements model.TelegramLinkSource.
func (s *Store) JoinInvite(ctx context.Context, hash string) (model.Chat, error) {
	api := s.api()
	if api == nil {
		return model.Chat{}, errors.New("offline")
	}
	res, err := api.MessagesImportChatInvite(ctx, hash)
	if tgerr.Is(err, "INVITE_HASH_EXPIRED", "INVITE_HASH_INVALID") {
		return model.Chat{}, model.ErrLinkNotFound
	}
	if err != nil {
		return model.Chat{}, joinError(err)
	}
	joined, ok := res.(*tg.MessagesChatInviteJoinResultOk)
	if !ok {
		return model.Chat{}, model.ErrJoinVerification
	}
	_ = s.Handle(ctx, joined.Updates)
	var chats []tg.ChatClass
	switch u := joined.Updates.(type) {
	case *tg.Updates:
		chats = u.Chats
	case *tg.UpdatesCombined:
		chats = u.Chats
	}
	for _, raw := range chats {
		if inv, err := s.inviteChat(raw); err == nil {
			return *inv.Chat, nil
		}
	}
	return model.Chat{}, model.ErrLinkNotFound
}

// UserByID implements model.TelegramLinkSource for mentions without a
// username: their messages bring the user.
func (s *Store) UserByID(_ context.Context, id int64) (model.Chat, error) {
	chat := peerID(&tg.PeerUser{UserID: id})
	s.history.mu.Lock()
	peer, ok := s.history.peers[chat]
	s.history.mu.Unlock()
	if !ok || peer.Hash == 0 && !peer.Rights.Self {
		return model.Chat{}, model.ErrLinkNotFound
	}
	return s.chatFor(chat, nil), nil
}

// SenderChat implements model.SenderChatSource. A member a supergroup
// brought only as a min user has no access hash: Telegram gives it for
// the message that names them, as Telegram Desktop asks for it.
func (s *Store) SenderChat(ctx context.Context, m model.Message) (model.Chat, error) {
	if model.IsChannelID(m.SenderID) && m.SenderID != m.Key.ChatID {
		return s.senderChannel(ctx, m)
	}
	if m.SenderID <= 0 {
		return model.Chat{}, model.ErrLinkNotFound
	}
	chat, err := s.UserByID(ctx, m.SenderID)
	if err == nil || !errors.Is(err, model.ErrLinkNotFound) {
		return chat, err
	}
	c := s.history
	c.mu.Lock()
	api, group := c.api, c.peers[m.Key.ChatID]
	c.mu.Unlock()
	// Asked by the message of any chat: a group's member, or whom a
	// message forwarded there came from, in a private chat or Saved
	// Messages too.
	if group.Kind == "" {
		return model.Chat{}, model.ErrLinkNotFound
	}
	if api == nil {
		return model.Chat{}, errors.New("offline")
	}
	users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserFromMessage{
		Peer: group.input(), MsgID: int(m.Key.MessageID), UserID: m.SenderID,
	}})
	if tgerr.Is(err, "USER_ID_INVALID", "PEER_ID_INVALID", "MSG_ID_INVALID", "CHANNEL_PRIVATE") {
		return model.Chat{}, model.ErrLinkNotFound
	}
	if err != nil {
		return model.Chat{}, err
	}
	for _, raw := range users {
		if u, ok := raw.(*tg.User); ok && u.ID == m.SenderID && !u.Min && (u.AccessHash != 0 || u.Self) {
			s.rememberPeers([]tg.UserClass{u}, nil)
			return s.chatFor(peerID(&tg.PeerUser{UserID: u.ID}), nil), nil
		}
	}
	return model.Chat{}, model.ErrLinkNotFound
}

// senderChannel is the channel that posted m in a group. Its history is
// open to all, joined or not, unless the account is banned from it:
// Telegram tells which, and asked by the message, knows a channel the
// group brought only as a min one. Banned, what the cache has of it is
// still shown; ErrChatInaccessible only when it has nothing. Without
// Telegram's answer, a channel known is opened as it is.
func (s *Store) senderChannel(ctx context.Context, m model.Message) (model.Chat, error) {
	id := -1000000000000 - m.SenderID
	c := s.history
	c.mu.Lock()
	api, group, peer := c.api, c.peers[m.Key.ChatID], c.peers[m.SenderID]
	c.mu.Unlock()
	known := peer.Kind == "channel"
	var input tg.InputChannelClass = &tg.InputChannelFromMessage{Peer: group.input(), MsgID: int(m.Key.MessageID), ChannelID: id}
	if known && peer.Rights.Known && peer.Hash != 0 {
		input = &tg.InputChannel{ChannelID: id, AccessHash: peer.Hash}
	} else if group.Kind == "" {
		input = nil
	}
	if api == nil || input == nil {
		if known {
			return s.chatFor(m.SenderID, nil), nil
		}
		return model.Chat{}, model.ErrLinkNotFound
	}
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{input})
	switch {
	case tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHANNEL_PUBLIC_GROUP_NA"):
	case err != nil && known:
		return s.chatFor(m.SenderID, nil), nil
	case err != nil:
		return model.Chat{}, err
	default:
		for _, raw := range res.GetChats() {
			switch ch := raw.(type) {
			case *tg.Channel:
				if ch.ID == id {
					s.rememberPeers(nil, []tg.ChatClass{ch})
					return s.chatFor(m.SenderID, nil), nil
				}
			case *tg.ChannelForbidden:
				if ch.ID == id {
					s.rememberPeers(nil, []tg.ChatClass{ch})
				}
			}
		}
	}
	chat := s.chatFor(m.SenderID, nil)
	if chat.Title == "" {
		chat.Title, chat.Kind = m.SenderName, model.KindChannel
	}
	if s.cachedHistory(ctx, m.SenderID) {
		return chat, nil
	}
	return chat, model.ErrChatInaccessible
}

// cachedHistory tells whether the cache has any of chat's history.
func (s *Store) cachedHistory(ctx context.Context, chat int64) bool {
	c := s.history
	c.mu.Lock()
	h, cache := c.histories[chat], c.cache
	c.mu.Unlock()
	if h != nil && len(h.Messages) > 0 {
		return true
	}
	if cache == nil {
		return false
	}
	msgs, err := cache.Around(ctx, chat, 0, 1)
	return err == nil && len(msgs) > 0
}

func (s *Store) api() *tg.Client {
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	return s.history.api
}
