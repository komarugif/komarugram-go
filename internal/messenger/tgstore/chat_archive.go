// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"

	"github.com/gotd/td/tg"
)

// SetChatArchived moves a dialog and applies Telegram's confirmed membership.
func (s *Store) SetChatArchived(ctx context.Context, chat int64, archived bool) error {
	s.archiveWrite.Lock()
	defer s.archiveWrite.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c := s.history
	c.mu.Lock()
	api, peer := c.api, c.peers[chat]
	c.mu.Unlock()
	if api == nil || peer.ID == 0 {
		return errNotConnected
	}
	folderID := 0
	if archived {
		folderID = 1
	}
	updates, err := api.FoldersEditPeerFolders(ctx, []tg.InputFolderPeer{{Peer: peer.input(), FolderID: folderID}})
	if err != nil {
		return err
	}
	return s.Handle(ctx, updates)
}
