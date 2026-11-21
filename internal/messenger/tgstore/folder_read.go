// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// Start with both dialog lists, before community collapsing, UI pagination
// or muting the navigation badge. entries and chats are immutable snapshots.
func (s *Store) UnreadInFolder(id int64) []model.FolderReadChat {
	// Never offer a bulk read based on just the first pages of a new account.
	if !s.dialogsComplete.Load() {
		return nil
	}
	s.mu.RLock()
	folders, chats, entries := s.folders, s.chats, s.entries
	s.mu.RUnlock()
	var folder model.Folder
	if id != 0 {
		i := slices.IndexFunc(folders, func(f model.Folder) bool { return f.ID == id })
		if i < 0 {
			return nil
		}
		folder = folders[i]
	}
	h := s.history
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []model.FolderReadChat
	for _, c := range chats {
		if c.Kind == model.KindCommunity {
			continue
		}
		if c.Forum {
			if f := h.forums[c.ID]; f != nil {
				var unread, mentions int
				for _, t := range f.topics {
					unread += t.Unread
					mentions += t.Mentions
				}
				c.Unread, c.UnreadMentions = max(c.Unread, unread), max(c.UnreadMentions, mentions)
			}
		}
		marked := entries[c.ID].unreadMark
		if (c.Unread > 0 || c.UnreadMentions > 0 || marked) && folder.Contains(c) {
			out = append(out, model.FolderReadChat{ID: c.ID, Top: max(c.Last.ID, model.MessageID(h.top[c.ID])), Unread: c.Unread, Mentions: c.UnreadMentions, Marked: marked, Forum: c.Forum})
		}
	}
	return out
}

// A user-requested read is explicit even in Ghost mode. Wait for each reply
// before publishing it and before sending the next request.
func (s *Store) ReadFolderChats(ctx context.Context, chats []model.FolderReadChat) error {
	h := s.history
	h.mu.Lock()
	if h.api == nil || h.closing {
		h.mu.Unlock()
		return errNotConnected
	}
	api, session := h.api, h.ctx
	h.wg.Add(1)
	h.mu.Unlock()
	defer h.wg.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	var failure error
	acknowledged := make(map[int64]model.FolderReadChat, len(chats))
	var refresh []int64
	for _, chat := range chats {
		if err := ctx.Err(); err != nil {
			failure = err
			break
		}
		if err := s.readFolderChat(ctx, api, &chat); err != nil {
			failure = err
			if _, flood := tgerr.AsFloodWait(err); flood || ctx.Err() != nil {
				break
			}
			// A server may accept the history read before refusing the unread mark.
			refresh = append(refresh, chat.ID)
			continue
		}
		acknowledged[chat.ID] = chat
	}
	if len(acknowledged) > 0 {
		refresh = append(refresh, s.recordFolderReads(acknowledged)...)
		s.changed()
	}
	for batch := range slices.Chunk(refresh, 100) {
		s.addDialogs(batch)
	}
	// Save acknowledged work even when closing the window cancelled the rest.
	if len(acknowledged) == 0 {
		return failure
	}
	saveCtx, stopSave := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stopSave()
	return errors.Join(failure, s.persistDialogs(saveCtx))
}

func (s *Store) readFolderChat(ctx context.Context, api *tg.Client, chat *model.FolderReadChat) error {
	h := s.history
	h.mu.Lock()
	peer := h.peers[chat.ID]
	h.mu.Unlock()
	if peer.ID == 0 {
		return errNotConnected
	}
	if chat.Forum && (chat.Unread > 0 || chat.Mentions > 0) {
		if err := s.readFolderForum(ctx, api, peer, chat.Top); err != nil {
			return err
		}
	} else if chat.Unread > 0 {
		if chat.Top <= 0 {
			res, err := api.MessagesGetPeerDialogs(ctx, []tg.InputDialogPeerClass{peer.dialogPeer()})
			if err != nil {
				return err
			}
			for _, raw := range res.Dialogs {
				if d, ok := raw.(*tg.Dialog); ok && peerID(d.Peer) == chat.ID {
					chat.Top = model.MessageID(d.TopMessage)
					break
				}
			}
			if chat.Top <= 0 {
				return errors.New("missing dialog top message")
			}
		}
		if err := readPeerHistory(ctx, api, peer, chat.Top); err != nil {
			return err
		}
	}
	if chat.Mentions > 0 && !chat.Forum {
		if err := readFolderMentions(ctx, api, peer, 0); err != nil {
			return err
		}
	}
	if chat.Marked {
		ok, err := api.MessagesMarkDialogUnread(ctx, &tg.MessagesMarkDialogUnreadRequest{Peer: peer.dialogPeer()})
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("clearing unread mark refused")
		}
	}
	return nil
}

func readFolderMentions(ctx context.Context, api *tg.Client, peer peerRecord, topic int) error {
	req := &tg.MessagesReadMentionsRequest{Peer: peer.input()}
	if topic != 0 {
		req.SetTopMsgID(topic)
	}
	for {
		response, err := api.MessagesReadMentions(ctx, req)
		if err != nil {
			return err
		}
		if response.Offset == 0 {
			return nil
		}
	}
}

// Publish a single snapshot to every window, retaining newer incoming messages
// and protecting acknowledged inbox positions from dialog pages already in flight.
func (s *Store) recordFolderReads(read map[int64]model.FolderReadChat) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ghost.mu.Lock()
	defer s.ghost.mu.Unlock()
	if s.ghost.read == nil {
		s.ghost.read = map[int64]model.MessageID{}
	}
	var refresh []int64
	var entries map[int64]entry
	chats := slices.Clone(s.chats)
	for i, c := range chats {
		target, ok := read[c.ID]
		if !ok {
			continue
		}
		if target.Marked && s.entries[c.ID].unreadMark {
			if entries == nil {
				entries = maps.Clone(s.entries)
			}
			e := entries[c.ID]
			e.unreadMark = false
			entries[c.ID] = e
		}
		if c.Last.ID <= target.Top {
			chats[i].Unread, chats[i].UnreadMentions = 0, 0
		} else {
			refresh = append(refresh, c.ID)
		}
		if target.Unread > 0 && !target.Forum {
			chats[i].ReadInbox = max(c.ReadInbox, target.Top)
			s.ghost.read[c.ID] = max(s.ghost.read[c.ID], target.Top)
		}
	}
	s.chats = chats
	if entries != nil {
		s.entries = entries
		s.folders = folderList(s.filterSelf, s.filters.Filters, entries)
	}
	return refresh
}

func (s *Store) readFolderForum(ctx context.Context, api *tg.Client, peer peerRecord, top model.MessageID) error {
	req := &tg.MessagesGetForumTopicsRequest{Peer: peer.input(), Limit: topicPage}
	seen := map[int]bool{}
	for {
		res, err := api.MessagesGetForumTopics(ctx, req)
		if err != nil {
			return err
		}
		if len(res.Topics) == 0 {
			return nil
		}
		before := len(seen)
		dates := map[int]int{}
		if !res.OrderByCreateDate {
			for _, m := range res.Messages {
				if d, ok := m.(interface{ GetDate() int }); ok {
					dates[m.GetID()] = d.GetDate()
				}
			}
		}
		for _, raw := range res.Topics {
			topic, ok := raw.(*tg.ForumTopic)
			if !ok {
				continue
			}
			req.OffsetID, req.OffsetTopic, req.OffsetDate = topic.TopMessage, topic.ID, topic.Date
			if date := dates[topic.TopMessage]; date != 0 {
				req.OffsetDate = date
			}
			if seen[topic.ID] {
				continue
			}
			seen[topic.ID] = true
			if topic.UnreadCount == 0 && topic.UnreadMentionsCount == 0 {
				continue
			}
			until := topic.TopMessage
			if top > 0 {
				until = min(until, int(top))
			}
			if until < topic.ID {
				continue
			}
			ok, err = api.MessagesReadDiscussion(ctx, &tg.MessagesReadDiscussionRequest{Peer: peer.input(), MsgID: topic.ID, ReadMaxID: until})
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("topic read refused")
			}
			if topic.UnreadMentionsCount > 0 {
				if err := readFolderMentions(ctx, api, peer, topic.ID); err != nil {
					return err
				}
			}
			s.history.mu.Lock()
			s.history.readTopic(peer.key(), topic.ID, until)
			s.history.mu.Unlock()
		}
		if len(seen) >= res.Count {
			return nil
		}
		if len(seen) == before {
			return errors.New("forum topic pagination stalled")
		}
	}
}
