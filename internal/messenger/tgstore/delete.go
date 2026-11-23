// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tg"
)

func (s *Store) DeleteMessages(ctx context.Context, chat int64, ids []model.MessageID, revoke bool) error {
	chat = s.realChat(chat)
	c := s.history
	c.mu.Lock()
	api := c.api
	peer, ok := c.peers[chat]
	c.mu.Unlock()
	if api == nil {
		return errors.New("cannot delete while offline")
	}
	if !ok {
		return errors.New("unknown chat")
	}
	if peer.Kind == "channel" && !revoke {
		return errors.New("channel messages can only be deleted for everyone")
	}
	var all, kept []int
	seen := map[int]bool{}
	for _, id := range ids {
		n := int(id)
		if n <= 0 {
			return errors.New("invalid message ID")
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		if s.keptDeleted(ctx, chat, id) {
			kept = append(kept, n)
		} else {
			all = append(all, n)
		}
	}
	if len(kept) > 0 {
		// Telegram deleted them already: they go from this computer.
		scope := int64(0)
		if peer.Kind == "channel" {
			scope = chat
		}
		tops, err := s.deleteMessages(ctx, scope, kept)
		if err != nil {
			return err
		}
		s.replaceDeletedPreviews(ctx, tops)
		s.changed()
	}
	for len(all) > 0 {
		n := min(len(all), 100)
		batch := all[:n]
		var err error
		if peer.Kind == "channel" {
			_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, ID: batch})
		} else {
			_, err = api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{ID: batch, Revoke: revoke})
		}
		if err != nil {
			return fmt.Errorf("delete: %w", err)
		}
		scope := int64(0)
		if peer.Kind == "channel" {
			scope = chat
		}
		tops, err := s.deleteMessages(ctx, scope, batch)
		if err != nil {
			return err
		}
		s.replaceDeletedPreviews(ctx, tops)
		s.changed()
		all = all[n:]
	}
	return s.persistDialogs(ctx)
}

// keptDeleted reports whether message id of chat is one Telegram deleted
// that the cache keeps.
func (s *Store) keptDeleted(ctx context.Context, chat int64, id model.MessageID) bool {
	cache := s.Cache()
	if cache == nil {
		return false
	}
	m, ok, err := cache.Message(ctx, chat, int(id))
	return err == nil && ok && m.Deleted
}

// MessageErased implements model.ErasedMessages: the message was deleted
// and is no longer in its history. One kept deleted is still there.
func (s *Store) MessageErased(key model.MessageKey) bool {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleted[key] || key.ChatID > -1000000000000 && c.globalDeleted[int(key.MessageID)]
}
