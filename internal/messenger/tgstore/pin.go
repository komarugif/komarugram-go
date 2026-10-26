// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// Pinned chats stay at the top of the list, in the order they were pinned
// (messages.toggleDialogPin). Only the main list is read, so the pins of
// other folders and of the archive are not told of here.

// PinChat implements model.ChatListActions.
func (s *Store) PinChat(ctx context.Context, chat int64, pin bool) error {
	c := s.history
	c.mu.Lock()
	api, peer := c.api, c.peers[chat]
	c.mu.Unlock()
	if api == nil || peer.ID == 0 {
		return errNotConnected
	}
	_, err := api.MessagesToggleDialogPin(ctx, &tg.MessagesToggleDialogPinRequest{Pinned: pin, Peer: peer.dialogPeer()})
	switch {
	case tgerr.Is(err, "PINNED_DIALOGS_TOO_MUCH"):
		return model.ErrPinnedTooMuch
	case err != nil:
		return err
	}
	s.publish(func() { s.chats = model.WithPinned(s.chats, chat, pin) })
	// What the list looks like offline follows.
	_ = s.persistDialogs(ctx)
	return nil
}

// ReorderPinned implements model.PinReorderer with
// messages.reorderPinnedDialogs, as Telegram Desktop saves the order of the
// main list (forced: a chat pinned elsewhere meanwhile and not in order is
// unpinned). The list has the order before Telegram answers, and the one it
// had back when Telegram refuses it.
func (s *Store) ReorderPinned(ctx context.Context, order []int64) error {
	c := s.history
	c.mu.Lock()
	api := c.api
	peers := make([]tg.InputDialogPeerClass, 0, len(order))
	for _, id := range order {
		if peer := c.peers[id]; peer.ID != 0 {
			peers = append(peers, peer.dialogPeer())
		}
	}
	c.mu.Unlock()
	if api == nil || len(peers) != len(order) {
		return errNotConnected
	}
	s.mu.Lock()
	was := model.PinnedOrder(s.chats)
	same := model.SamePinned(s.chats, order)
	if same {
		s.chats = model.WithPinOrder(s.chats, order)
	}
	s.mu.Unlock()
	if !same {
		return model.ErrPinnedChanged
	}
	s.changed()
	if _, err := api.MessagesReorderPinnedDialogs(ctx, &tg.MessagesReorderPinnedDialogsRequest{Force: true, Order: peers}); err != nil {
		s.publish(func() {
			// Unless the pinned chats changed meanwhile, as an update
			// tells: their order is then the update's.
			if model.SamePinned(s.chats, was) {
				s.chats = model.WithPinOrder(s.chats, was)
			}
		})
		return err
	}
	_ = s.persistDialogs(ctx)
	return nil
}

// dialogPeer is the dialog of p, as pins name it: a community's is its own.
func (p peerRecord) dialogPeer() tg.InputDialogPeerClass {
	if p.Kind == "community" {
		return &tg.InputDialogPeerCommunity{Community: &tg.InputChannel{ChannelID: p.ID, AccessHash: p.Hash}}
	}
	return &tg.InputDialogPeer{Peer: p.input()}
}

// dialogPeerID is the chat id of the dialog p names, 0 for none known.
func dialogPeerID(p tg.DialogPeerClass) int64 {
	switch p := p.(type) {
	case *tg.DialogPeer:
		return peerID(p.Peer)
	case *tg.DialogPeerCommunity:
		return peerID(&tg.PeerChannel{ChannelID: p.CommunityID})
	}
	return 0
}

// applyPin puts a pin or unpin that an update tells of in the list.
func (s *Store) applyPin(u *tg.UpdateDialogPinned) {
	if folder, ok := u.GetFolderID(); ok && folder != 0 {
		return
	}
	id := dialogPeerID(u.Peer)
	if id == 0 {
		return
	}
	s.mu.Lock()
	s.chats = model.WithPinned(s.chats, id, u.Pinned)
	s.mu.Unlock()
}

// applyPinOrder puts the order of the pinned chats that an update tells of in
// the list; without the order, the update only says that it changed, and the
// pinned chats are read again.
func (s *Store) applyPinOrder(ctx context.Context, u *tg.UpdatePinnedDialogs) {
	if folder, ok := u.GetFolderID(); ok && folder != 0 {
		return
	}
	order, ok := u.GetOrder()
	if !ok {
		s.readPinned(ctx)
		return
	}
	var ids []int64
	for _, p := range order {
		if id := dialogPeerID(p); id != 0 {
			ids = append(ids, id)
		}
	}
	s.mu.Lock()
	s.chats = model.WithPinOrder(s.chats, ids)
	s.mu.Unlock()
}

// readPinned reads which chats are pinned, and in what order.
func (s *Store) readPinned(ctx context.Context) {
	c := s.history
	c.mu.Lock()
	api := c.api
	c.mu.Unlock()
	if api == nil {
		return
	}
	res, err := api.MessagesGetPinnedDialogs(ctx, 0)
	if err != nil {
		return
	}
	var ids []int64
	for _, d := range res.Dialogs {
		switch d := d.(type) {
		case *tg.Dialog:
			if d.Pinned {
				ids = append(ids, peerID(d.Peer))
			}
		case *tg.DialogCommunity:
			if d.Pinned {
				ids = append(ids, peerID(&tg.PeerChannel{ChannelID: d.CommunityID}))
			}
		}
	}
	s.mu.Lock()
	s.chats = model.WithPinOrder(s.chats, ids)
	s.mu.Unlock()
}

// MarkChatRead implements model.ChatListActions: everything up to the chat's
// last message is read, whatever Ghost says.
func (s *Store) MarkChatRead(chat int64) {
	c := s.history
	c.mu.Lock()
	top := c.top[chat]
	c.mu.Unlock()
	if top > 0 {
		s.MarkRead(chat, model.MessageID(top), true)
	}
}
