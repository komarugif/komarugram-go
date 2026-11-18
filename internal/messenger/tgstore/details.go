// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"

	"komarugram/internal/messenger/model"
)

// ChatDetails implements model.ChatDetailer.
func (s *Store) ChatDetails(chat int64) model.ChatDetails {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	peer := c.peers[chat]
	d := model.ChatDetails{Members: peer.Metadata.Members, MembersKnown: peer.Metadata.MembersKnown}
	if photo := peer.Photo; photo != nil {
		d.PhotoDC = photo.DC
	}
	return d
}

// Online implements model.ChatDetailer with messages.getOnlines, which
// counts every member, unlike Telegram Desktop's count of the members it
// loaded.
func (s *Store) Online(ctx context.Context, chat int64) (int, error) {
	c := s.history
	c.mu.Lock()
	api, peer := c.api, c.peers[chat]
	c.mu.Unlock()
	if api == nil {
		return 0, errNotConnected
	}
	if peer.ID == 0 || peer.Kind == "user" || peer.Rights.Broadcast {
		return 0, nil
	}
	res, err := api.MessagesGetOnlines(ctx, peer.input())
	if err != nil {
		return 0, err
	}
	return res.Onlines, nil
}
