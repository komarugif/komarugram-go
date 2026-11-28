// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

func TestSetChatArchived(t *testing.T) {
	s := testStore(t)
	s.chats = []model.Chat{{ID: 9, Pinned: true}}
	s.history.peers[9] = peerRecord{ID: 9, Kind: "user", Hash: 17}
	rejected := errors.New("archive rejected")
	folder, reject := 1, true
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch req := in.(type) {
		case *tg.FoldersEditPeerFoldersRequest:
			if len(req.FolderPeers) != 1 {
				return nil, fmt.Errorf("wrong peers: %v", req.FolderPeers)
			}
			move := req.FolderPeers[0]
			peer, ok := move.Peer.(*tg.InputPeerUser)
			if move.FolderID != folder || !ok || peer.UserID != 9 || peer.AccessHash != 17 {
				return nil, fmt.Errorf("wrong archive move: %v", move)
			}
			if reject {
				return nil, rejected
			}
			return &tg.UpdateShort{Update: &tg.UpdateFolderPeers{FolderPeers: []tg.FolderPeer{{Peer: &tg.PeerUser{UserID: 9}, FolderID: folder}}}}, nil
		case *tg.MessagesGetPeerDialogsRequest:
			return &tg.MessagesPeerDialogs{}, nil
		default:
			return nil, fmt.Errorf("unexpected RPC %T", in)
		}
	})
	if err := s.SetChatArchived(t.Context(), 9, true); !errors.Is(err, rejected) || len(s.Chats()) != 1 {
		t.Fatalf("rejected move changed the list or lost the error: %v", err)
	}
	reject = false
	if err := s.SetChatArchived(t.Context(), 9, true); err != nil {
		t.Fatal(err)
	}
	if chats := s.FolderDialogs(); len(s.Chats()) != 0 || len(chats) != 1 || !chats[0].Archived || chats[0].Pinned {
		t.Fatalf("archived dialog: %v", chats)
	}
	folder = 0
	if err := s.SetChatArchived(t.Context(), 9, false); err != nil {
		t.Fatal(err)
	}
	if chats := s.Chats(); len(chats) != 1 || chats[0].ID != 9 || chats[0].Archived {
		t.Fatalf("restored dialog: %v", chats)
	}
}
