// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// The load of the dialogs stops at the first page that tells nothing new
// (loadDialogs): the list is in the order of the chats' last messages, so
// the pages after it are of chats that changed no more than it did, which
// this client keeps from the load before. What changes a chat deeper in the
// list without moving it comes as an update: a read, a move to the archive,
// a mark as unread. The chats the pages do not bring that are older than
// the oldest they did stay too: Telegram stops handing out the oldest chats
// of a long list, which exist all the same, and Telegram for Android keeps
// them as well.

// keptEntry is an entry as the cache keeps it.
type keptEntry struct {
	Contact    bool `json:",omitempty"`
	UnreadMark bool `json:",omitempty"`
}

func keepEntries(entries map[int64]entry) map[int64]keptEntry {
	kept := make(map[int64]keptEntry, len(entries))
	for id, e := range entries {
		if e != (entry{}) {
			kept[id] = keptEntry{Contact: e.contact, UnreadMark: e.unreadMark}
		}
	}
	return kept
}

func readEntries(kept map[int64]keptEntry) map[int64]entry {
	entries := make(map[int64]entry, len(kept))
	for id, e := range kept {
		entries[id] = entry{contact: e.Contact, unreadMark: e.UnreadMark}
	}
	return entries
}

// sameChat reports whether a chat as a page of dialogs tells it (c, e) is
// the one kept (k, ke), as far as the list shows.
func sameChat(c model.Chat, e entry, k model.Chat, ke entry) bool {
	return c.Unread == k.Unread && c.ReadInbox == k.ReadInbox && c.ReadOutbox == k.ReadOutbox &&
		c.Pinned == k.Pinned && c.Muted == k.Muted && c.Last.ID == k.Last.ID && e == ke &&
		c.Community == k.Community && c.Collapsed == k.Collapsed
}

// pageUnchanged reports whether each chat of page, which l read last, is
// the one kept now. The caller holds s.mu.
func (s *Store) pageUnchanged(page []model.Chat, l *list) bool {
	if len(page) == 0 {
		return false
	}
	kept := make(map[int64]model.Chat, len(s.chats))
	for _, c := range s.chats {
		kept[c.ID] = c
	}
	for _, c := range page {
		k, ok := kept[c.ID]
		if !ok || !sameChat(c, l.entries[c.ID], k, s.entries[c.ID]) {
			return false
		}
	}
	return true
}

// horizon is the time of the oldest last message of the chats l read, but
// the pinned, which come first whatever their age, and communities, which
// have no messages of their own; ok is false for none.
func (l *list) horizon() (oldest time.Time, ok bool) {
	for _, c := range l.chats {
		if c.Pinned || c.Kind == model.KindCommunity {
			continue
		}
		if !ok || c.LastTime.Before(oldest) {
			oldest, ok = c.LastTime, true
		}
	}
	return oldest, ok
}

// withKept is the chats l read, followed by the chats kept now that its
// pages did not bring and whose last message is no newer than theirs, the
// newest first: the chats of the pages not read, and those Telegram no
// longer hands out. A chat not brought and newer is gone: deleted, left or
// archived meanwhile. Kept chats are not pinned, as the pinned come in the
// first page. The caller holds s.mu.
func (s *Store) withKept(l *list) ([]model.Chat, map[int64]entry) {
	chats := slices.Clone(l.chats)
	entries := maps.Clone(l.entries)
	horizon, bounded := l.horizon()
	var kept []model.Chat
	for _, c := range s.chats {
		if l.seen[c.ID] || bounded && c.LastTime.After(horizon) {
			continue
		}
		c.Pinned, c.PinRank = false, 0
		kept = append(kept, c)
		if e, ok := s.entries[c.ID]; ok {
			entries[c.ID] = e
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].LastTime.After(kept[j].LastTime) })
	return append(chats, kept...), entries
}

// folderList is the folders filters make, over chats whose entries are
// entries.
func folderList(self int64, filters []tg.DialogFilterClass, entries map[int64]entry) []model.Folder {
	l := &list{self: self}
	var folders []model.Folder
	for _, f := range filters {
		if folder, ok := l.folder(f, entries); ok {
			folders = append(folders, folder)
		}
	}
	return folders
}

// applyUnreadMark keeps a chat's mark as unread, which the folders that
// leave read chats out look at.
func (s *Store) applyUnreadMark(u *tg.UpdateDialogUnreadMark) {
	p, ok := u.Peer.(*tg.DialogPeer)
	if !ok {
		return
	}
	id := peerID(p.Peer)
	s.publish(func() {
		entries := maps.Clone(s.entries)
		if entries == nil {
			entries = map[int64]entry{}
		}
		e := entries[id]
		e.unreadMark = u.Unread
		entries[id] = e
		s.entries = entries
		s.folders = folderList(s.me.ID, s.filters, entries)
	})
}

// applyFolderPeers takes the chats moved to the archive out of the list,
// and asks Telegram for the dialogs of those moved back: a load of the
// dialogs may stop before their place.
func (s *Store) applyFolderPeers(u *tg.UpdateFolderPeers) {
	archived := map[int64]bool{}
	var back []int64
	for _, p := range u.FolderPeers {
		if p.FolderID == 0 {
			back = append(back, peerID(p.Peer))
		} else {
			archived[peerID(p.Peer)] = true
		}
	}
	if len(archived) > 0 {
		s.publish(func() {
			s.chats = slices.DeleteFunc(slices.Clone(s.chats), func(c model.Chat) bool { return archived[c.ID] })
		})
	}
	if len(back) > 0 {
		s.addDialogs(back)
	}
	if len(archived) > 0 || len(back) > 0 {
		s.goPersistDialogs()
	}
}

// addDialogs asks Telegram for the dialogs of chats, and puts them in the
// list.
func (s *Store) addDialogs(chats []int64) {
	c := s.history
	c.mu.Lock()
	api, ctx := c.api, c.ctx
	var peers []tg.InputDialogPeerClass
	for _, id := range chats {
		if p := c.peers[id]; p.ID != 0 {
			peers = append(peers, &tg.InputDialogPeer{Peer: p.input()})
		}
	}
	closing := c.closing
	c.mu.Unlock()
	if api == nil || closing || len(peers) == 0 {
		return
	}
	c.wg.Go(func() {
		defer crash.Recover("dialogs added", nil)
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		res, err := api.MessagesGetPeerDialogs(ctx, peers)
		if err != nil {
			return
		}
		s.rememberPeers(res.Users, res.Chats)
		s.mu.RLock()
		self := s.me.ID
		s.mu.RUnlock()
		l := newList(self)
		l.add(&tg.MessagesDialogs{Dialogs: res.Dialogs, Messages: res.Messages, Chats: res.Chats, Users: res.Users})
		s.publish(func() {
			chats := slices.Clone(s.chats)
			entries := maps.Clone(s.entries)
			if entries == nil {
				entries = map[int64]entry{}
			}
			for _, added := range l.chats {
				if i := slices.IndexFunc(chats, func(c model.Chat) bool { return c.ID == added.ID }); i >= 0 {
					chats[i] = added
				} else {
					chats = append(chats, added)
				}
				entries[added.ID] = l.entries[added.ID]
			}
			model.SortChats(chats)
			s.chats, s.entries = chats, entries
			s.folders = folderList(self, s.filters, entries)
		})
		_ = s.persistDialogs(ctx)
	})
}

// goPersistDialogs keeps the dialogs off the caller's goroutine.
func (s *Store) goPersistDialogs() {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.cache == nil {
		return
	}
	ctx := c.ctx
	c.wg.Go(func() {
		defer crash.Recover("dialogs kept", nil)
		_ = s.persistDialogs(ctx)
	})
}
