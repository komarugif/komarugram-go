// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"math"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// DirectMessages implements model.DirectMessagesSource: the chat of the
// channel's direct messages, as the channel names it. A chat the store
// has not met yet comes with the channel's full info, as Telegram Desktop
// asks for it (Session::applyMonoforumLinkedId).
func (s *Store) DirectMessages(ctx context.Context, chat int64) (model.Chat, error) {
	s.history.mu.Lock()
	id := s.history.peers[chat].Rights.DirectMessagesID
	monoforum, known := s.history.peers[id]
	s.history.mu.Unlock()
	if id != 0 && known && monoforum.Hash != 0 {
		return monoforum.withMetadata(model.Chat{ID: id}), nil
	}
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return model.Chat{}, err
	}
	defer done()
	if peer.Kind != "channel" {
		return model.Chat{}, errors.New("not a channel")
	}
	full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
	if err != nil {
		return model.Chat{}, err
	}
	s.rememberPeers(full.Users, full.Chats)
	s.history.mu.Lock()
	id = s.history.peers[chat].Rights.DirectMessagesID
	monoforum, known = s.history.peers[id]
	s.history.mu.Unlock()
	if id == 0 {
		return model.Chat{}, errors.New("the channel takes no direct messages")
	}
	if !known {
		return model.Chat{}, errors.New("direct messages unavailable")
	}
	s.changed()
	return monoforum.withMetadata(model.Chat{ID: id}), nil
}

// ChatMuted implements model.NotificationSetter, as the chat list knows it.
func (s *Store) ChatMuted(chat int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.chats {
		if c.ID == chat {
			return c.Muted
		}
	}
	return false
}

// SetChatMuted implements model.NotificationSetter with
// account.updateNotifySettings: muted for ever, as Telegram Desktop's Mute
// of a channel, or not at all.
func (s *Store) SetChatMuted(ctx context.Context, chat int64, muted bool) error {
	chat = s.realChat(chat)
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return err
	}
	defer done()
	until := 0
	if muted {
		until = math.MaxInt32
	}
	settings := tg.InputPeerNotifySettings{}
	settings.SetMuteUntil(until)
	if _, err := api.AccountUpdateNotifySettings(ctx, &tg.AccountUpdateNotifySettingsRequest{Peer: &tg.InputNotifyPeer{Peer: peer.input()}, Settings: settings}); err != nil {
		return err
	}
	applied := tg.PeerNotifySettings{}
	applied.SetMuteUntil(until)
	s.setMuted(chat, applied)
	s.changed()
	return nil
}
